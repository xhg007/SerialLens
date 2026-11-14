package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"seriallens/internal/agent"
	"seriallens/internal/events"
	"seriallens/internal/llm"
)

// AIAPI AI 助手（PRD §4）：LLM 接入 + 多轮对话 + 工具调用 + 会话落盘。
//
// 阶段一（M5-1）只做「能连通、能多轮、能流式、能停」；阶段二把工具调用循环
// 接进来：LLM 通过 12 个白名单工具读写工程上下文，正式脚本必须由用户在
// Diff 卡片里点「应用」才会落盘。
type AIAPI struct {
	s     *Services
	store *llm.Store

	mu      sync.Mutex
	conv    []llm.Message // 只存 user/assistant 文本（system 每次现拼，避免上下文过期）
	running bool
	runID   string
	cancel  context.CancelFunc

	sess        *agent.Session
	sessProject string

	pending map[string]chan bool // 确认 id → 用户决定（UI 回填）
}

// NewAIService 构造 AI 服务；home 为空时用 ~/.seriallens。
func NewAIService(s *Services, home string) *AIAPI {
	return &AIAPI{s: s, store: llm.NewStore(home), pending: map[string]chan bool{}}
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
// 带上 running，页面重载后 UI 才不会以为「空闲」而允许再发一条。
func (a *AIAPI) GetAISettings() map[string]any {
	st, err := a.store.Load()
	out := st.Masked()
	if err != nil {
		out["warning"] = err.Error()
	}
	a.mu.Lock()
	out["running"] = a.running
	out["messages"] = len(a.conv)
	a.mu.Unlock()
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

// SendAIMessage 发起一轮对话（默认任务 A：脚本生成/修复）。
func (a *AIAPI) SendAIMessage(text string) (string, error) {
	return a.SendAIMessageAs(text, string(agent.TaskScript))
}

// SendAIMessageAs 指定任务（script / charts）；白名单由 agent 包强制，
// 任务 B 的模型拿不到任何写脚本的工具。
func (a *AIAPI) SendAIMessageAs(text, task string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("消息不能为空")
	}
	tk := agent.Task(task)
	if tk != agent.TaskScript && tk != agent.TaskCharts {
		tk = agent.TaskScript
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
	if err := a.ensureExternalNotice(st); err != nil {
		return "", err
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
	history := append([]llm.Message{}, a.conv...)
	a.mu.Unlock()

	a.emit(events.EvAIStart, map[string]any{
		"id": runID, "provider": st.Provider, "model": st.Model, "task": string(tk),
	})
	a.emitStatus()

	go a.run(ctx, cancel, runID, st, tk, text, history)
	return runID, nil
}

// run 执行「流式 + 工具调用回喂」循环，把增量/工具事件/确认请求推给前端。
func (a *AIAPI) run(ctx context.Context, cancel context.CancelFunc, runID string, st llm.Settings, task agent.Task, text string, history []llm.Message) {
	defer cancel()

	sess := a.sessionFor(text, st.Model)
	exec := agent.NewExecutor(&agentHost{s: a.s}, a.confirmer(runID))
	temp := st.Temperature
	runner := &agent.Runner{
		Client:      llm.NewClient(st),
		Model:       st.Model,
		MaxTokens:   st.MaxTokens,
		Temperature: &temp,
		Exec:        exec,
		Session:     sess,
		Emit:        a.emit,
		RunID:       runID,
	}
	msgs := runner.BuildMessages(task, a.buildContext(), history)

	// 合批推送：token 级事件直接推给 WebView 会造成事件风暴（100Hz 数据流已经占满通道）。
	// force=true 用于补最后一段，避免「回答太快、一帧都没推」。
	var bufText, bufReason strings.Builder
	lastFlush := time.Now()
	flushedT, flushedR := 0, 0
	flush := func(force bool) {
		if !force && time.Since(lastFlush) < 60*time.Millisecond {
			return
		}
		t, r := bufText.String(), bufReason.String()
		dt, dr := t[flushedT:], r[flushedR:]
		if dt == "" && dr == "" {
			return
		}
		flushedT, flushedR = len(t), len(r)
		lastFlush = time.Now()
		a.emit(events.EvAIDelta, map[string]any{"id": runID, "text": dt, "reasoning": dr})
	}

	start := time.Now()
	res, err := runner.Run(ctx, task, msgs, func(content, reasoning string) error {
		bufText.WriteString(content)
		bufReason.WriteString(reasoning)
		flush(false)
		return nil
	})
	flush(true)

	elapsed := time.Since(start).Milliseconds()
	canceled := ctx.Err() == context.Canceled

	a.mu.Lock()
	a.running = false
	a.cancel = nil
	if res != nil && res.Content != "" {
		a.conv = append(a.conv, llm.Message{
			Role: "assistant", Content: res.Content, CreatedAt: time.Now().UnixMilli(),
		})
		a.trimLocked()
	}
	a.mu.Unlock()

	if err != nil && !canceled && !strings.Contains(err.Error(), "context canceled") {
		a.emit(events.EvAIError, map[string]any{"id": runID, "message": llm.FriendlyError(err)})
		a.emitStatus()
		return
	}

	payload := map[string]any{
		"id": runID, "canceled": canceled, "elapsed_ms": elapsed,
	}
	if res != nil {
		payload["content"] = res.Content
		payload["reasoning"] = res.Reasoning
		payload["rounds"] = res.Rounds
		payload["tool_calls"] = res.ToolCalls
		payload["finish_reason"] = res.FinishText
		if res.FirstMS > 0 {
			payload["first_ms"] = res.FirstMS
		}
		if res.Usage != nil {
			payload["usage"] = map[string]any{
				"prompt_tokens":     res.Usage.PromptTokens,
				"completion_tokens": res.Usage.CompletionTokens,
				"total_tokens":      res.Usage.TotalTokens,
			}
		}
	}
	if sess != nil {
		payload["session_id"] = sess.ID()
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

// ClearAIConversation 清空上下文并结束当前会话文件（下一轮会新建）。
func (a *AIAPI) ClearAIConversation() error {
	a.mu.Lock()
	a.conv = nil
	a.sess = nil
	a.sessProject = ""
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

// ---------- 工具调用：确认桥 / 上下文 / 会话 ----------

// ensureExternalNotice 首次发送前的数据外发告知（PRD §4.8）。
//
// 用系统原生对话框（Wails v3 MessageDialog）拦一次：用户点「继续」才落盘 notice_ack，
// 之后不再打扰。本地端点（Ollama/自建 localhost）不外发数据，直接放行。
func (a *AIAPI) ensureExternalNotice(st llm.Settings) error {
	if st.NoticeAck || llm.IsLocalEndpoint(st.BaseURL) {
		return nil
	}
	app := application.Get()
	if app == nil {
		return nil // 无 UI 场景（CLI/测试）不拦
	}

	approved := false
	dlg := app.Dialog.Question().SetTitle("数据外发提示")
	okBtn := dlg.AddButton("继续")
	okBtn.OnClick(func() { approved = true })
	cancelBtn := dlg.AddButton("取消")
	cancelBtn.SetAsCancel()
	if win := app.Window.Current(); win != nil {
		dlg.AttachToWindow(win)
	}
	dlg.SetDefaultButton(okBtn).SetCancelButton(cancelBtn)
	dlg.SetMessage(fmt.Sprintf(
		"AI 助手会把当前脚本、样本帧摘要和你的提问发送到 %s（模型 %s），用于生成解码脚本。\n\n是否继续？",
		llm.EndpointHost(st.BaseURL), st.Model,
	))
	dlg.Show()

	if !approved {
		return fmt.Errorf("已取消：未确认数据外发，未发送任何内容")
	}
	if err := a.store.MarkNoticeAck(); err != nil {
		return nil // 记录失败不影响使用（下次再问一次而已）
	}
	return nil
}

// ResolveAIConfirm UI 回填确认结果（应用/放弃、写入/不写入）。
// ResolveAIConfirm 处理确认卡片的用户决定（阻塞中的工具立即恢复）。
func (a *AIAPI) ResolveAIConfirm(id string, approved bool) error {
	a.mu.Lock()
	ch := a.pending[id]
	a.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("确认请求已失效或已处理")
	}
	select {
	case ch <- approved:
	default: // 重复点击：忽略
	}
	return nil
}

// RecordUserAction 把用户的持久化动作写进当前会话 JSONL（PRD §4.6 回溯）。
// 由前端在确认卡片落定后调用：apply_script / apply_context_hints / reject_confirm。
// 写失败或无会话静默忽略——审计记录不能反过来打断用户操作。
func (a *AIAPI) RecordUserAction(action, detail string) {
	a.mu.Lock()
	sess := a.sess
	a.mu.Unlock()
	if sess == nil || action == "" {
		return
	}
	sess.Append(agent.SessionEvent{Type: agent.EvSessionUserAct, Action: action, Content: detail})
}

// confirmer 把 agent 的确认请求转成 UI 事件并阻塞等待。
func (a *AIAPI) confirmer(runID string) agent.Confirmer {
	return agent.ConfirmerFunc(func(ctx context.Context, req agent.ConfirmRequest) (bool, error) {
		ch := make(chan bool, 1)
		a.mu.Lock()
		if a.pending == nil {
			a.pending = map[string]chan bool{}
		}
		a.pending[req.ID] = ch
		a.mu.Unlock()
		defer func() {
			a.mu.Lock()
			delete(a.pending, req.ID)
			a.mu.Unlock()
		}()

		a.emit(events.EvAIConfirm, map[string]any{
			"id": req.ID, "run_id": runID, "kind": req.Kind,
			"title": req.Title, "description": req.Description,
			"target": req.Target, "content": truncateRunes(req.Content, 20000), "diff": req.Diff,
		})
		select {
		case v := <-ch:
			return v, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	})
}

// buildContext 组装本次请求的工程上下文（PRD §4.5.1）。
func (a *AIAPI) buildContext() agent.Context {
	c := agent.Context{}
	if st, err := a.store.Load(); err == nil {
		c.SystemExtra = st.SystemExtra
	}
	if a.s == nil {
		return c
	}
	if a.s.Project != nil {
		if dir, meta, ok := a.s.Project.svc.Current(); ok && meta != nil {
			c.ProjectName = meta.Name
			c.ContextHints = meta.AgentSettings.ContextHints
			if cfg := a.s.charts.Config(); cfg != nil {
				c.Charts, _ = chartsToMap(cfg)
			}
			c.Samples, _ = agent.NewSampleStore(dir).List(10)
		}
	}
	if a.s.scripts == nil {
		return c
	}
	c.ActiveScript = a.s.scripts.Active()
	c.Scripts = (&agentHost{s: a.s}).ScriptNames()
	if c.ActiveScript != "" {
		c.ScriptSource, _ = a.s.scripts.Read(c.ActiveScript)
	}
	return c
}

// sessionFor 复用/新建 JSONL 会话（按工程隔离；切工程自动换文件）。
func (a *AIAPI) sessionFor(text, model string) *agent.Session {
	dir := ""
	if a.s != nil && a.s.Project != nil {
		if d, _, ok := a.s.Project.svc.Current(); ok {
			dir = d
		}
	}
	if dir == "" {
		return nil
	}

	a.mu.Lock()
	if a.sess != nil && a.sessProject == dir {
		sess := a.sess
		a.mu.Unlock()
		sess.Append(agent.SessionEvent{Type: agent.EvSessionMessage, Role: "user", Content: text})
		return sess
	}
	a.mu.Unlock()

	sess, err := agent.NewSession(dir, "")
	if err != nil {
		return nil
	}
	sess.Append(agent.SessionEvent{Type: agent.EvSessionMeta, Model: model, Title: firstLine(text, 40)})
	sess.Append(agent.SessionEvent{Type: agent.EvSessionMessage, Role: "user", Content: text})

	a.mu.Lock()
	a.sess, a.sessProject = sess, dir
	a.mu.Unlock()

	a.emit(events.EvAISession, map[string]any{"session_id": sess.ID(), "path": sess.Path()})
	return sess
}

// ListAISessions 当前工程的会话列表（按更新时间倒序）。
func (a *AIAPI) ListAISessions() ([]agent.SessionSummary, error) {
	dir := ""
	if a.s != nil && a.s.Project != nil {
		if d, _, ok := a.s.Project.svc.Current(); ok {
			dir = d
		}
	}
	if dir == "" {
		return nil, nil
	}
	return agent.ListSessions(dir)
}

// LoadAISession 载入历史会话（恢复对话上下文 + 返回事件流给前端渲染）。
func (a *AIAPI) LoadAISession(id string) (map[string]any, error) {
	dir := ""
	if a.s != nil && a.s.Project != nil {
		if d, _, ok := a.s.Project.svc.Current(); ok {
			dir = d
		}
	}
	if dir == "" {
		return nil, fmt.Errorf("no project open")
	}
	sess, err := agent.NewSession(dir, id)
	if err != nil {
		return nil, err
	}
	events, err := agent.LoadEvents(sess.Path())
	if err != nil {
		return nil, err
	}

	conv := make([]llm.Message, 0, len(events))
	for _, e := range events {
		if e.Type != agent.EvSessionMessage || e.Content == "" {
			continue
		}
		if e.Role != "user" && e.Role != "assistant" {
			continue
		}
		conv = append(conv, llm.Message{Role: e.Role, Content: e.Content})
	}

	a.mu.Lock()
	a.conv = conv
	a.sess, a.sessProject = sess, dir
	a.mu.Unlock()
	a.emitStatus()
	return map[string]any{"id": sess.ID(), "path": sess.Path(), "events": events}, nil
}

// GetAISessionInfo 当前会话 id（前端 header 显示/高亮用）。
func (a *AIAPI) GetAISessionInfo() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sess == nil {
		return map[string]any{"id": "", "path": ""}
	}
	return map[string]any{"id": a.sess.ID(), "path": a.sess.Path()}
}

// ---------- 内部 ----------

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

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
