package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"seriallens/internal/events"
	"seriallens/internal/llm"
	"seriallens/internal/lua"
)

// AIAPI AI 助手（PRD §4 / M5 第一阶段：LLM 接入 + 多轮对话）。
//
// 本阶段只做「能连通、能多轮、能流式、能停」；工具调用与双任务状态机在下一阶段
// 接在这条链路上（ChatStream 已支持 tool_calls 聚合，协议先就位）。
type AIAPI struct {
	s     *Services
	store *llm.Store

	mu      sync.Mutex
	conv    []llm.Message // 只存 user/assistant（system 每次现拼，避免上下文过期）
	running bool
	runID   string
	cancel  context.CancelFunc
}

// NewAIService 构造 AI 服务；home 为空时用 ~/.seriallens。
func NewAIService(s *Services, home string) *AIAPI {
	return &AIAPI{s: s, store: llm.NewStore(home)}
}

// ServiceStartup 注入事件桥接（install 幂等）。
func (a *AIAPI) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	a.s.install(application.Get())
	return nil
}

// AIProvider 供应商预设（前端下拉用）。
type AIProvider = llm.Provider

// GetAIProviders 返回内置供应商预设。
func (a *AIAPI) GetAIProviders() []AIProvider {
	return llm.Providers()
}

// GetAISettings 返回脱敏后的配置（永远不含 Key 明文）。
func (a *AIAPI) GetAISettings() map[string]any {
	st, err := a.store.Load()
	out := st.Masked()
	if err != nil {
		out["warning"] = err.Error()
	}
	return out
}

// AISettingsInput 来自前端的配置更新（零值字段不覆盖）。
type AISettingsInput struct {
	Provider    string   `json:"provider"`
	BaseURL     string   `json:"base_url"`
	Model       string   `json:"model"`
	APIKey      string   `json:"api_key"`
	Temperature *float64 `json:"temperature"` // 指针：0 也是合法值（确定性输出）
	MaxTokens   int      `json:"max_tokens"`
	TimeoutSec  int      `json:"timeout_sec"`
	Enabled     *bool    `json:"enabled"`
	SystemExtra string   `json:"system_extra"`
	ClearKey    bool     `json:"clear_key"`
}

// SaveAISettings 合并保存配置；APIKey 为空表示沿用已存的 Key。
func (a *AIAPI) SaveAISettings(in AISettingsInput) (map[string]any, error) {
	cur, _ := a.store.Load()
	next := cur
	if in.Provider != "" {
		next.Provider = in.Provider
		// 换供应商但没填 base_url 时跟随预设，避免带着上一家的地址发请求
		if preset := llm.ProviderByID(in.Provider); in.BaseURL == "" && preset.ID == in.Provider {
			next.BaseURL = preset.BaseURL
			if in.Model == "" && len(preset.Models) > 0 {
				next.Model = preset.Models[0]
			}
		}
	}
	if in.BaseURL != "" {
		next.BaseURL = in.BaseURL
	}
	if in.Model != "" {
		next.Model = in.Model
	}
	if in.APIKey != "" {
		next.APIKey = strings.TrimSpace(in.APIKey)
		next.KeyFromEnv = false
	}
	if in.Temperature != nil {
		next.Temperature = *in.Temperature
	}
	if in.MaxTokens > 0 {
		next.MaxTokens = in.MaxTokens
	}
	if in.TimeoutSec > 0 {
		next.TimeoutSec = in.TimeoutSec
	}
	if in.Enabled != nil {
		next.Enabled = *in.Enabled
	}
	next.SystemExtra = in.SystemExtra

	if in.ClearKey {
		next.APIKey = ""
		if err := a.store.ClearKey(); err != nil {
			return nil, err
		}
	}
	if err := a.store.Save(next); err != nil {
		return nil, err
	}
	a.emitStatus()
	reloaded, _ := a.store.Load()
	return reloaded.Masked(), nil
}

// TestAIConnection 用当前（或传入的）配置做连通性测试。
//
// 优先打 GET /models（零 token），不支持再退化成一次极短对话。
func (a *AIAPI) TestAIConnection(in AISettingsInput) (map[string]any, error) {
	cur, _ := a.store.Load()
	probe := cur
	if in.Provider != "" {
		probe.Provider = in.Provider
		if preset := llm.ProviderByID(in.Provider); in.BaseURL == "" {
			probe.BaseURL = preset.BaseURL
			probe.Model = preset.Models[0]
		}
	}
	if in.BaseURL != "" {
		probe.BaseURL = in.BaseURL
	}
	if in.Model != "" {
		probe.Model = in.Model
	}
	if in.APIKey != "" {
		probe.APIKey = strings.TrimSpace(in.APIKey)
	}
	if in.TimeoutSec > 0 {
		probe.TimeoutSec = in.TimeoutSec
	}
	probe = probe.Resolved()
	if probe.BaseURL == "" {
		return nil, fmt.Errorf("请先填写 base_url（OpenAI 兼容端点，通常以 /v1 结尾）")
	}
	if llm.ProviderByID(probe.Provider).NeedsKey && probe.APIKey == "" {
		return nil, fmt.Errorf("请先填写 API Key")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	client := llm.NewClient(probe)
	res, err := client.Ping(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s", llm.FriendlyError(err))
	}
	return map[string]any{
		"ok":         true,
		"via":        res.Via,
		"latency_ms": res.LatencyMS,
		"model":      res.Model,
		"endpoint":   res.Endpoint,
		"models":     res.Models,
		"reply":      res.Reply,
	}, nil
}

// SendAIMessage 发起一轮对话（立即返回 run id，正文通过 ai:delta 事件流式返回）。
func (a *AIAPI) SendAIMessage(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("消息不能为空")
	}
	st, err := a.store.Load()
	if err != nil {
		return "", err
	}
	if !st.Enabled {
		return "", fmt.Errorf("AI 助手已停用（可在设置里重新启用）")
	}
	if !st.Ready() {
		return "", fmt.Errorf("AI 尚未配置完成：请选择供应商、填好 Base URL 与 API Key")
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return "", fmt.Errorf("上一条消息还在生成中，请先停止或等待完成")
	}
	runID := fmt.Sprintf("ai-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), st.Timeout())
	a.running = true
	a.runID = runID
	a.cancel = cancel
	a.conv = append(a.conv, llm.Message{Role: "user", Content: text, CreatedAt: time.Now().UnixMilli()})
	messages := a.historyLocked()
	a.mu.Unlock()

	a.emit(events.EvAIStart, map[string]any{
		"id": runID, "provider": st.Provider, "model": st.Model,
	})
	a.emitStatus()

	go a.run(ctx, cancel, runID, st, messages)
	return runID, nil
}

// run 执行一次流式调用并把增量推给前端。
func (a *AIAPI) run(ctx context.Context, cancel context.CancelFunc, runID string, st llm.Settings, messages []llm.Message) {
	defer cancel()

	client := llm.NewClient(st)
	start := time.Now()
	var content, reasoning strings.Builder
	lastFlush := time.Now()
	flushedText, flushedReasoning := 0, 0

	// 合批推送：token 级事件直接推给 WebView 会造成事件风暴（100Hz 数据流已经占满通道）。
	// force=true 用于补最后一段，避免「回答太快、一帧都没推」的情况。
	emitDelta := func(force bool) {
		if !force && time.Since(lastFlush) < 60*time.Millisecond {
			return
		}
		text := content.String()[flushedText:]
		reason := reasoning.String()[flushedReasoning:]
		if text == "" && reason == "" {
			return
		}
		flushedText, flushedReasoning = content.Len(), reasoning.Len()
		lastFlush = time.Now()
		a.emit(events.EvAIDelta, map[string]any{
			"id": runID, "text": text, "reasoning": reason,
		})
	}

	temp := st.Temperature
	resp, err := client.ChatStream(ctx, llm.ChatRequest{
		Model:       st.Model,
		Messages:    messages,
		Temperature: &temp,
		MaxTokens:   st.MaxTokens,
	}, func(d llm.Delta) error {
		if d.Content != "" {
			content.WriteString(d.Content)
		}
		if d.Reasoning != "" {
			reasoning.WriteString(d.Reasoning)
		}
		emitDelta(false)
		return nil
	})
	emitDelta(true)

	elapsed := time.Since(start).Milliseconds()
	canceled := ctx.Err() == context.Canceled

	a.mu.Lock()
	a.running = false
	a.cancel = nil
	finalContent := content.String()
	if finalContent == "" && resp != nil {
		// 极端情况：只有 finish 帧、正文靠非流式兜底（少数代理会这样）
		finalContent = resp.Content()
	}
	if reasoning.Len() == 0 && resp != nil {
		reasoning.WriteString(resp.Reasoning())
	}
	if finalContent != "" {
		a.conv = append(a.conv, llm.Message{
			Role: "assistant", Content: finalContent, ReasoningContent: reasoning.String(),
			CreatedAt: time.Now().UnixMilli(),
		})
		a.trimLocked()
	}
	a.mu.Unlock()

	if err != nil && !canceled && !strings.Contains(err.Error(), "context canceled") {
		msg := llm.FriendlyError(err)
		a.emit(events.EvAIError, map[string]any{"id": runID, "message": msg})
		a.emitStatus()
		return
	}

	payload := map[string]any{
		"id": runID, "content": finalContent, "reasoning": reasoning.String(),
		"elapsed_ms": elapsed, "canceled": canceled,
	}
	if resp != nil {
		payload["first_ms"] = resp.FirstDeltaMS
		payload["finish_reason"] = resp.FinishReason()
		payload["model"] = resp.Model
		if resp.Usage != nil {
			payload["usage"] = map[string]any{
				"prompt_tokens":     resp.Usage.PromptTokens,
				"completion_tokens": resp.Usage.CompletionTokens,
				"total_tokens":      resp.Usage.TotalTokens,
			}
		}
	}
	a.emit(events.EvAIDone, payload)
	a.emitStatus()
}

// CancelAI 停止当前生成（已输出内容会保留在会话里）。
func (a *AIAPI) CancelAI() error {
	a.mu.Lock()
	cancel := a.cancel
	a.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	return nil
}

// ClearAIConversation 清空会话上下文（不影响设置）。
func (a *AIAPI) ClearAIConversation() error {
	a.mu.Lock()
	a.conv = nil
	a.mu.Unlock()
	a.emitStatus()
	return nil
}

// GetAIConversation 返回当前会话（页面重载后恢复用）。
func (a *AIAPI) GetAIConversation() []map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]map[string]any, 0, len(a.conv))
	for _, m := range a.conv {
		out = append(out, map[string]any{
			"role":      m.Role,
			"content":   m.Content,
			"reasoning": m.ReasoningContent,
			"ts":        m.CreatedAt,
		})
	}
	return out
}

// GetAIStatus AI 面板状态（是否在生成、配置是否齐全）。
func (a *AIAPI) GetAIStatus() map[string]any {
	st, _ := a.store.Load()
	masked := st.Masked()
	a.mu.Lock()
	running, msgs := a.running, len(a.conv)
	a.mu.Unlock()
	masked["running"] = running
	masked["messages"] = msgs
	return masked
}

// ---------- 内部 ----------

// historyLocked 组装 system + 历史（调用方持锁）。
func (a *AIAPI) historyLocked() []llm.Message {
	out := make([]llm.Message, 0, len(a.conv)+2)
	if sys := a.systemPrompt(); sys != "" {
		out = append(out, llm.Message{Role: "system", Content: sys})
	}
	out = append(out, a.conv...)
	return out
}

// systemPrompt 每次现拼：脚本内容会变，缓存的 prompt 会误导模型。
func (a *AIAPI) systemPrompt() string {
	st, _ := a.store.Load()
	var b strings.Builder
	b.WriteString("你是 SerialLens 的串口协议脚本助手。SerialLens 通过 Lua 脚本把串口字节流解析成结构化字段并绘图。\n")
	b.WriteString("职责边界：只做「生成/修复 Lua 解码脚本」和「解释字段含义」；与串口协议无关的请求，用一句话说明不支持并引导回协议话题。\n")
	b.WriteString("数据流：串口原始字节先按 detect_frame（可选）切帧，再以**字符串**（Lua 的 string，按字节索引、下标从 1 开始）传给 on_frame(raw)。\n")
	b.WriteString("要求：\n")
	b.WriteString("1. 输出可直接运行的完整 Lua 代码，放在 ```lua 代码块里；字段必须是 table（字段名 -> 值）。\n")
	b.WriteString("2. 数值字段用 emit_chart 推给图表时才需要图表配置；不确定通道名就沿用已有通道。\n")
	b.WriteString("3. 不要编造不存在的内置函数；只能用下面清单里的 API。\n")
	b.WriteString("4. 解释保持简短（3 句以内），代码优先。\n")

	b.WriteString("\n可用内置 API：\n")
	for _, d := range lua.APIManifest() {
		b.WriteString("- ")
		b.WriteString(d.Call)
		b.WriteString(" → ")
		b.WriteString(d.Returns)
		b.WriteString("；")
		b.WriteString(d.Doc)
		b.WriteString("\n")
	}

	if name, src := a.currentScript(); src != "" {
		b.WriteString("\n当前脚本（")
		b.WriteString(name)
		b.WriteString("）：\n```lua\n")
		b.WriteString(truncateRunes(src, 6000))
		b.WriteString("\n```\n")
	}
	if extra := strings.TrimSpace(st.SystemExtra); extra != "" {
		b.WriteString("\n工程偏好：")
		b.WriteString(extra)
		b.WriteString("\n")
	}
	return b.String()
}

// currentScript 取当前工程的活动脚本（没有工程/脚本时返回空）。
func (a *AIAPI) currentScript() (string, string) {
	if a.s == nil || a.s.scripts == nil {
		return "", ""
	}
	name := a.s.scripts.Active()
	if name == "" {
		return "", ""
	}
	src, err := a.s.scripts.Read(name)
	if err != nil {
		return name, ""
	}
	return name, src
}

// trimLocked 控制上下文长度：保留最近 24 条（约 4~8 轮），超长单条会整条保留。
func (a *AIAPI) trimLocked() {
	const maxMessages = 24
	if len(a.conv) > maxMessages {
		a.conv = a.conv[len(a.conv)-maxMessages:]
	}
}

func (a *AIAPI) emit(ev string, payload map[string]any) {
	if a.s == nil {
		return
	}
	a.s.emitEvent(ev, payload)
}

func (a *AIAPI) emitStatus() {
	if a.s == nil {
		return
	}
	a.s.emitEvent(events.EvAIStatus, a.GetAIStatus())
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n-- …（已截断）"
}
