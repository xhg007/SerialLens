package agent

import (
	"context"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"seriallens/internal/llm"
)

// 工具执行参数上限（PRD §4.10：单次工具 5s）。
const (
	toolTimeout     = 5 * time.Second
	maxSamplesPerOp = 10
)

// Tool 一个可被模型调用的工具。
type Tool struct {
	Name   string
	Desc   string
	Schema map[string]any
	Tasks  []Task
	// Timeout 单次执行上限；0 表示默认 5s。
	// 需要等用户点确认的工具（request_user_confirm / set_context_hints）要放大，
	// 否则用户还在看 diff，工具已经超时了。
	Timeout time.Duration
	Run     func(ctx context.Context, ex *Executor, args map[string]any) (any, error)
}

// confirmTimeout 等待用户确认的上限（超过视为放弃，避免会话永远挂住）。
const confirmTimeout = 10 * time.Minute

// ToolResult 一次工具调用的结果（既回喂模型，也推给前端时间线）。
type ToolResult struct {
	CallID     string `json:"call_id"`
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	DurationMS int64  `json:"duration_ms"`
	Result     any    `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Executor 工具执行上下文：只持有 Host 与确认器，无全局状态。
type Executor struct {
	host    Host
	confirm Confirmer
	samples *SampleStore

	mu        sync.Mutex
	lastDraft *draft
}

type draft struct {
	name string
	src  string
	path string
}

// NewExecutor 绑定 Host；projectDir 用于读样本目录。
func NewExecutor(host Host, confirm Confirmer) *Executor {
	dir := ""
	if host != nil {
		dir = host.ProjectDir()
	}
	return &Executor{host: host, confirm: confirm, samples: NewSampleStore(dir)}
}

// SetConfirmer 运行中替换确认器（UI 重连后重新绑定）。
func (e *Executor) SetConfirmer(c Confirmer) {
	e.mu.Lock()
	e.confirm = c
	e.mu.Unlock()
}

// LastDraft 返回最近一次草稿（确认卡片用它渲染 diff）。
func (e *Executor) LastDraft() (name, src string, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lastDraft == nil {
		return "", "", false
	}
	return e.lastDraft.name, e.lastDraft.src, true
}

// Registry 工具注册表（按任务过滤）。
type Registry struct {
	byName map[string]*Tool
	order  []*Tool
}

// NewRegistry 构建 12 个工具（PRD §4.4）。白名单之外的工具名一律拒绝。
func NewRegistry() *Registry {
	r := &Registry{byName: map[string]*Tool{}}
	add := func(t *Tool) {
		r.byName[t.Name] = t
		r.order = append(r.order, t)
	}
	for _, t := range tools() {
		add(t)
	}
	return r
}

// Get 按名字取工具。
func (r *Registry) Get(name string) (*Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// Defs 返回某任务可用的工具声明（下发给模型）。
func (r *Registry) Defs(task Task) []llm.Tool {
	out := make([]llm.Tool, 0, len(r.order))
	for _, t := range r.order {
		if !taskAllows(t.Tasks, task) {
			continue
		}
		out = append(out, llm.Tool{
			Type: "function",
			Function: llm.ToolDef{
				Name:        t.Name,
				Description: t.Desc,
				Parameters:  t.Schema,
			},
		})
	}
	return out
}

// Names 某任务可用工具名（日志/评测用）。
func (r *Registry) Names(task Task) []string {
	out := make([]string, 0, len(r.order))
	for _, t := range r.order {
		if taskAllows(t.Tasks, task) {
			out = append(out, t.Name)
		}
	}
	sort.Strings(out)
	return out
}

func taskAllows(tasks []Task, task Task) bool {
	for _, t := range tasks {
		if t == task {
			return true
		}
	}
	return false
}

// Execute 执行一次工具调用：参数解析 → 白名单 → 任务校验 → 超时执行。
func (e *Executor) Execute(ctx context.Context, task Task, call llm.ToolCall) ToolResult {
	start := time.Now()
	name := call.Function.Name
	res := ToolResult{CallID: call.ID, Name: name}

	tool, ok := NewRegistry().Get(name) // 注册表无状态，构造开销可忽略
	if !ok {
		res.Error = fmt.Sprintf("unknown tool %q", name)
		return res
	}
	if !taskAllows(tool.Tasks, task) {
		res.Error = fmt.Sprintf("tool %q is not available for task %q", name, task)
		return res
	}

	args := map[string]any{}
	if s := strings.TrimSpace(call.Function.Arguments); s != "" {
		if err := jsonUnmarshal([]byte(s), &args); err != nil {
			res.Error = fmt.Sprintf("invalid arguments JSON: %v", err)
			return res
		}
	}

	timeout := tool.Timeout
	if timeout <= 0 {
		timeout = toolTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan struct{})
	var out any
	var err error
	go func() {
		defer close(done)
		out, err = tool.Run(runCtx, e, args)
	}()
	select {
	case <-done:
	case <-runCtx.Done():
		res.Error = fmt.Sprintf("tool %s timed out after %s", name, timeout)
		res.DurationMS = time.Since(start).Milliseconds()
		return res
	}

	res.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.OK = true
	res.Result = out
	return res
}

// ---------- 工具定义 ----------

func tools() []*Tool {
	both := []Task{TaskScript, TaskCharts}
	script := []Task{TaskScript}
	charts := []Task{TaskCharts}

	return []*Tool{
		{
			Name:  "compile_lua",
			Desc:  "编译检查 Lua 源码（不落盘）。返回 {ok, diagnostic}；diagnostic 含 line/column/message。",
			Tasks: script,
			Schema: obj(map[string]any{
				"script_name": str("脚本名，默认 draft.lua"),
				"source":      str("完整 Lua 源码"),
			}, "source"),
			Run: toolCompile,
		},
		{
			Name:  "run_lua_on_samples",
			Desc:  "在沙箱里用给定源码逐条跑 on_frame，返回每条样本的字段与图表点。沙箱禁用 send_bytes/os/io。",
			Tasks: script,
			Schema: obj(map[string]any{
				"source":     str("完整 Lua 源码"),
				"sample_ids": arr("样本 id 列表；为空则自动取最近 3 条"),
			}, "source"),
			Run: toolRunSamples,
		},
		{
			Name:  "list_recent_samples",
			Desc:  "列出当前工程 .seriallens/samples/ 下的抓包样本（按时间倒序，含 HEX 预览）。",
			Tasks: both,
			Schema: obj(map[string]any{
				"limit":  num("最多返回条数，默认 10"),
				"filter": str("文件名子串过滤"),
			}),
			Run: toolListSamples,
		},
		{
			Name:  "read_sample_frame",
			Desc:  "读取单条样本的完整 HEX 与字节数。",
			Tasks: both,
			Schema: obj(map[string]any{
				"sample_id": str("样本 id（list_recent_samples 返回的文件名）"),
			}, "sample_id"),
			Run: toolReadSample,
		},
		{
			Name:  "save_script_draft",
			Desc:  "把脚本源码保存为草稿（只写 .seriallens/drafts/，绝不覆盖正式脚本）。应用需用户确认。",
			Tasks: script,
			Schema: obj(map[string]any{
				"name":   str("草稿文件名，如 temp_decode.lua"),
				"source": str("完整 Lua 源码"),
			}, "name", "source"),
			Run: toolSaveDraft,
		},
		{
			Name:   "read_charts_config",
			Desc:   "读取当前 charts.json（layout + widgets）。",
			Tasks:  both,
			Schema: obj(map[string]any{}),
			Run:    toolReadCharts,
		},
		{
			Name:  "update_charts_config",
			Desc:  "写入 charts.json（服务端 schema 校验，失败不落盘）。必须提交完整配置对象。",
			Tasks: charts,
			Schema: obj(map[string]any{
				"charts": map[string]any{"type": "object", "description": "完整 charts.json 内容"},
			}, "charts"),
			Run: toolUpdateCharts,
		},
		{
			Name:    "set_context_hints",
			Desc:    "把协议要点写入 project.json 的 agent_settings.context_hints（需用户确认）。",
			Tasks:   script,
			Timeout: confirmTimeout,
			Schema: obj(map[string]any{
				"hints": str("协议要点，纯文本，<=500 字"),
			}, "hints"),
			Run: toolSetHints,
		},
		{
			Name:    "request_user_confirm",
			Desc:    "请求用户确认（阻塞等待）。用于任何持久化动作前的关卡；附上方案说明与影响范围。",
			Tasks:   both,
			Timeout: confirmTimeout,
			Schema: obj(map[string]any{
				"title":       str("一句话标题"),
				"description": str("给用户看的说明：将做什么、影响什么"),
			}, "title", "description"),
			Run: toolRequestConfirm,
		},
		{
			Name:  "get_current_script",
			Desc:  "读取当前工程的脚本源码；不传 name 则读活动脚本。",
			Tasks: both,
			Schema: obj(map[string]any{
				"name": str("脚本名（相对 scripts/），默认活动脚本"),
			}),
			Run: toolGetScript,
		},
		{
			Name:   "list_builtin_api",
			Desc:   "列出内置 Lua API（struct_* 读取助手、emit_chart、band/bor 等）。",
			Tasks:  both,
			Schema: obj(map[string]any{}),
			Run:    toolListAPI,
		},
		{
			Name:  "read_recording_meta",
			Desc:  "读取 data/ 下 .srec 录制文件的元信息（端口/波特率/样本数），不能解码内容。",
			Tasks: both,
			Schema: obj(map[string]any{
				"file": str("文件名；为空则列出全部录制"),
			}),
			Run: toolReadRecording,
		},
	}
}

// ---------- 工具实现 ----------

func toolCompile(ctx context.Context, ex *Executor, args map[string]any) (any, error) {
	src := strArg(args, "source")
	name := strArg(args, "script_name")
	if name == "" {
		name = "draft.lua"
	}
	if err := ex.host.CompileScript(name, src); err != nil {
		return map[string]any{"ok": false, "diagnostic": diagOf(name, err), "error": err.Error()}, nil
	}
	return map[string]any{"ok": true}, nil
}

func toolRunSamples(ctx context.Context, ex *Executor, args map[string]any) (any, error) {
	src := strArg(args, "source")
	if strings.TrimSpace(src) == "" {
		return nil, fmt.Errorf("source is required")
	}
	ids := strSliceArg(args, "sample_ids")
	var raws [][]byte
	if len(ids) == 0 {
		list, err := ex.samples.List(3)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, fmt.Errorf("当前工程没有可用样本：请把 HEX 帧放到 %s/ 下，或直接把帧贴进对话", samplesDir)
		}
		for _, s := range list {
			ids = append(ids, s.ID)
		}
	}
	if len(ids) > maxSamplesPerOp {
		ids = ids[:maxSamplesPerOp]
	}
	for _, id := range ids {
		raw, err := ex.samples.Read(id)
		if err != nil {
			return nil, fmt.Errorf("read sample %s: %w", id, err)
		}
		raws = append(raws, raw)
	}

	outcomes := ex.host.RunSandbox(src, raws, toolTimeout)
	for i := range outcomes {
		if i < len(ids) {
			outcomes[i].SampleID = ids[i]
		}
	}
	if outcomes == nil {
		outcomes = []RunOutcome{}
	}
	return map[string]any{"ok": true, "samples": len(outcomes), "results": outcomes}, nil
}

func toolListSamples(ctx context.Context, ex *Executor, args map[string]any) (any, error) {
	limit := intArg(args, "limit", 10)
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	list, err := ex.samples.List(0)
	if err != nil {
		return nil, err
	}
	filter := strings.ToLower(strArg(args, "filter"))
	out := make([]SampleMeta, 0, limit)
	for _, s := range list {
		if filter != "" && !strings.Contains(strings.ToLower(s.Name), filter) {
			continue
		}
		out = append(out, s)
		if len(out) >= limit {
			break
		}
	}
	return map[string]any{"count": len(out), "dir": samplesDir, "samples": out}, nil
}

func toolReadSample(ctx context.Context, ex *Executor, args map[string]any) (any, error) {
	id := strArg(args, "sample_id")
	if id == "" {
		return nil, fmt.Errorf("sample_id is required")
	}
	raw, err := ex.samples.Read(id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"sample_id": id, "size": len(raw), "hex": strings.ToUpper(hex.EncodeToString(raw))}, nil
}

func toolSaveDraft(ctx context.Context, ex *Executor, args map[string]any) (any, error) {
	name := strArg(args, "name")
	if name == "" {
		name = "draft.lua"
	}
	if !strings.HasSuffix(strings.ToLower(name), ".lua") {
		name += ".lua"
	}
	src := strArg(args, "source")
	if strings.TrimSpace(src) == "" {
		return nil, fmt.Errorf("source is required")
	}
	if err := ex.host.CompileScript(name, src); err != nil {
		// 草稿也要能过编译：避免把烂脚本留进 drafts/ 让人误会
		return map[string]any{
			"ok": false, "saved": false,
			"diagnostic": diagOf(name, err),
			"hint":       "草稿未保存：先修掉编译错误",
		}, nil
	}
	path, err := ex.host.SaveDraft(name, src)
	if err != nil {
		return nil, err
	}
	ex.mu.Lock()
	ex.lastDraft = &draft{name: name, src: src, path: path}
	ex.mu.Unlock()
	return map[string]any{"ok": true, "saved": true, "draft": path, "name": name}, nil
}

func toolReadCharts(ctx context.Context, ex *Executor, args map[string]any) (any, error) {
	cfg, err := ex.host.Charts()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return map[string]any{"charts": nil, "note": "当前工程还没有 charts.json"}, nil
	}
	return map[string]any{"charts": cfg}, nil
}

func toolUpdateCharts(ctx context.Context, ex *Executor, args map[string]any) (any, error) {
	raw, ok := args["charts"]
	if !ok {
		return nil, fmt.Errorf("charts object is required")
	}
	cfg, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("charts must be an object")
	}
	if err := ex.host.SaveCharts(cfg); err != nil {
		return map[string]any{"ok": false, "error": err.Error(), "hint": "schema 校验失败，未落盘"}, nil
	}
	return map[string]any{"ok": true}, nil
}

func toolSetHints(ctx context.Context, ex *Executor, args map[string]any) (any, error) {
	hints := strings.TrimSpace(strArg(args, "hints"))
	if hints == "" {
		return nil, fmt.Errorf("hints is required")
	}
	if len([]rune(hints)) > 500 {
		return nil, fmt.Errorf("hints too long (max 500 runes)")
	}
	if ex.confirm != nil {
		approved, err := ex.confirm.Confirm(ctx, ConfirmRequest{
			ID:          newID("cfm"),
			Kind:        "context_hints",
			Title:       "写入协议要点到工程配置？",
			Description: "将保存到 project.json 的 agent_settings.context_hints，之后的会话会自动带上。",
			Target:      "project.json",
			Content:     hints,
		})
		if err != nil {
			return nil, err
		}
		if !approved {
			return map[string]any{"ok": false, "approved": false, "note": "用户拒绝了写入"}, nil
		}
	}
	if err := ex.host.SetContextHints(hints); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "approved": true}, nil
}

func toolRequestConfirm(ctx context.Context, ex *Executor, args map[string]any) (any, error) {
	title := strArg(args, "title")
	desc := strArg(args, "description")
	if title == "" || desc == "" {
		return nil, fmt.Errorf("title and description are required")
	}
	req := ConfirmRequest{
		ID:          newID("cfm"),
		Kind:        "generic",
		Title:       title,
		Description: desc,
	}
	// 有草稿就把 diff 附上：用户在卡片里直接看到要改什么。
	// Target 显示「将被覆盖的正式脚本」，草稿名放描述里。
	if name, src, ok := ex.LastDraft(); ok {
		cur, _ := ex.host.ReadScript(ex.host.ActiveScript())
		req.Kind = "script_draft"
		req.Target = ex.host.ActiveScript()
		req.Content = src
		req.Description = desc + "\n\n（来源草稿：" + name + "）"
		if cur != "" {
			req.Diff = unifiedDiff(ex.host.ActiveScript(), cur, name, src)
		}
	}
	if ex.confirm == nil {
		return map[string]any{"approved": false, "note": "no UI attached"}, nil
	}
	approved, err := ex.confirm.Confirm(ctx, req)
	if err != nil {
		return nil, err
	}
	return map[string]any{"approved": approved}, nil
}

func toolGetScript(ctx context.Context, ex *Executor, args map[string]any) (any, error) {
	name := strArg(args, "name")
	if name == "" {
		name = ex.host.ActiveScript()
	}
	if name == "" {
		return nil, fmt.Errorf("当前工程没有活动脚本")
	}
	src, err := ex.host.ReadScript(name)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"name": name, "active": name == ex.host.ActiveScript(),
		"source": src, "scripts": ex.host.ScriptNames(),
	}, nil
}

func toolListAPI(ctx context.Context, ex *Executor, args map[string]any) (any, error) {
	return map[string]any{"api": ex.host.BuiltinAPI()}, nil
}

func toolReadRecording(ctx context.Context, ex *Executor, args map[string]any) (any, error) {
	list, err := ex.samples.Recordings()
	if err != nil {
		return nil, err
	}
	file := strArg(args, "file")
	if file == "" {
		return map[string]any{"count": len(list), "recordings": list}, nil
	}
	for _, r := range list {
		if strings.EqualFold(r.File, file) {
			return r, nil
		}
	}
	return nil, fmt.Errorf("recording %q not found in data/", file)
}

// ---------- 小工具 ----------

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		req := make([]any, 0, len(required))
		for _, r := range required {
			req = append(req, r)
		}
		s["required"] = req
	}
	return s
}

func str(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func num(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func arr(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

func strArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func intArg(args map[string]any, key string, def int) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

func strSliceArg(args map[string]any, key string) []string {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func diagOf(name string, err error) any {
	d := diagFromError(name, err)
	return d
}
