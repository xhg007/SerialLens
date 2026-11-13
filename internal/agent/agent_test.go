package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"seriallens/internal/llm"
	"seriallens/internal/lua"
)

// fakeHost 内存实现，覆盖工具层需要的全部能力。
type fakeHost struct {
	dir         string
	scripts     map[string]string
	active      string
	compileErr  error
	charts      map[string]any
	savedCharts map[string]any
	drafts      map[string]string
	hints       string
	samples     map[string][]byte
	mu          sync.Mutex
}

func newFakeHost(t *testing.T) *fakeHost {
	t.Helper()
	dir := t.TempDir()
	// 样本目录真实落盘，顺带测 SampleStore
	if err := os.MkdirAll(filepath.Join(dir, ".seriallens", "samples"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".seriallens", "samples", "a.hex"), []byte("AA 06 01 02 03 04"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &fakeHost{
		dir:     dir,
		scripts: map[string]string{"main.lua": "function on_frame(raw) return { v = 1 } end\n"},
		active:  "main.lua",
		drafts:  map[string]string{},
	}
}

func (h *fakeHost) ProjectDir() string { return h.dir }
func (h *fakeHost) ScriptNames() []string {
	out := make([]string, 0, len(h.scripts))
	for k := range h.scripts {
		out = append(out, k)
	}
	return out
}
func (h *fakeHost) ActiveScript() string { return h.active }
func (h *fakeHost) ReadScript(name string) (string, error) {
	s, ok := h.scripts[name]
	if !ok {
		return "", fmt.Errorf("no script %s", name)
	}
	return s, nil
}
func (h *fakeHost) CompileScript(name, src string) error { return h.compileErr }
func (h *fakeHost) RunSandbox(src string, samples [][]byte, timeout time.Duration) []RunOutcome {
	out := make([]RunOutcome, 0, len(samples))
	for range samples {
		out = append(out, RunOutcome{OK: true, Fields: map[string]any{"v": 1}})
	}
	return out
}
func (h *fakeHost) Charts() (map[string]any, error) { return h.charts, nil }
func (h *fakeHost) SaveCharts(cfg map[string]any) error {
	h.mu.Lock()
	h.savedCharts = cfg
	h.mu.Unlock()
	return nil
}
func (h *fakeHost) SaveDraft(name, src string) (string, error) {
	h.mu.Lock()
	h.drafts[name] = src
	h.mu.Unlock()
	return ".seriallens/drafts/" + name, nil
}
func (h *fakeHost) SetContextHints(hints string) error {
	h.mu.Lock()
	h.hints = hints
	h.mu.Unlock()
	return nil
}
func (h *fakeHost) ListSamples(limit int) ([]SampleMeta, error) {
	return NewSampleStore(h.dir).List(limit)
}
func (h *fakeHost) ReadSample(id string) ([]byte, error) {
	return NewSampleStore(h.dir).Read(id)
}
func (h *fakeHost) ListRecordings() ([]RecordingMeta, error) {
	return NewSampleStore(h.dir).Recordings()
}
func (h *fakeHost) BuiltinAPI() []lua.APIDoc { return lua.APIManifest() }

func TestRegistryTaskIsolation(t *testing.T) {
	r := NewRegistry()

	if _, ok := r.Get("write_file"); ok {
		t.Fatal("write_file must not exist in the whitelist")
	}
	if _, ok := r.Get("send_serial"); ok {
		t.Fatal("send_serial must not exist in the whitelist")
	}
	if len(r.Names(TaskScript)) == 0 || len(r.Names(TaskCharts)) == 0 {
		t.Fatal("both tasks need tools")
	}

	// 任务 B 拿不到写脚本的工具
	for _, name := range r.Names(TaskCharts) {
		if name == "save_script_draft" || name == "compile_lua" {
			t.Fatalf("charts task must not get %s", name)
		}
	}
	// 任务 A 拿不到写图表的工具
	for _, name := range r.Names(TaskScript) {
		if name == "update_charts_config" {
			t.Fatalf("script task must not get %s", name)
		}
	}
}

func TestExecuteRejectsWrongTaskAndUnknownTool(t *testing.T) {
	ex := NewExecutor(newFakeHost(t), nil)
	ctx := context.Background()

	res := ex.Execute(ctx, TaskCharts, llm.ToolCall{ID: "1", Function: llm.ToolCallFunc{Name: "save_script_draft", Arguments: `{"name":"a.lua","source":"x"}`}})
	if res.OK || !strings.Contains(res.Error, "not available") {
		t.Fatalf("expected task rejection, got %+v", res)
	}

	res = ex.Execute(ctx, TaskScript, llm.ToolCall{ID: "2", Function: llm.ToolCallFunc{Name: "rm_rf", Arguments: `{}`}})
	if res.OK || !strings.Contains(res.Error, "unknown tool") {
		t.Fatalf("expected unknown tool, got %+v", res)
	}
}

func TestSaveDraftRefusesBrokenSource(t *testing.T) {
	h := newFakeHost(t)
	h.compileErr = fmt.Errorf("lua: compile draft.lua:3: unexpected symbol")
	ex := NewExecutor(h, nil)

	res := ex.Execute(context.Background(), TaskScript, llm.ToolCall{
		ID: "1", Function: llm.ToolCallFunc{Name: "save_script_draft", Arguments: `{"name":"d.lua","source":"bad"}`},
	})
	if !res.OK {
		t.Fatalf("tool should return structured failure, got %+v", res)
	}
	m, _ := res.Result.(map[string]any)
	if m["saved"] != false {
		t.Fatalf("draft must not be saved when compile fails: %+v", m)
	}
	if len(h.drafts) != 0 {
		t.Fatalf("no draft expected, got %v", h.drafts)
	}
}

func TestSetContextHintsNeedsConfirm(t *testing.T) {
	h := newFakeHost(t)
	ex := NewExecutor(h, ConfirmerFunc(func(ctx context.Context, req ConfirmRequest) (bool, error) {
		if req.Kind != "context_hints" {
			t.Fatalf("kind = %q", req.Kind)
		}
		return false, nil
	}))

	res := ex.Execute(context.Background(), TaskScript, llm.ToolCall{
		ID: "1", Function: llm.ToolCallFunc{Name: "set_context_hints", Arguments: `{"hints":"AA 帧头"}`},
	})
	if !res.OK {
		t.Fatalf("result = %+v", res)
	}
	m, _ := res.Result.(map[string]any)
	if m["approved"] != false {
		t.Fatalf("expected rejected: %+v", m)
	}
	if h.hints != "" {
		t.Fatalf("hints must not be written when rejected, got %q", h.hints)
	}
}

func TestRequestConfirmAttachesDraftDiff(t *testing.T) {
	h := newFakeHost(t)
	var got ConfirmRequest
	ex := NewExecutor(h, ConfirmerFunc(func(ctx context.Context, req ConfirmRequest) (bool, error) {
		got = req
		return true, nil
	}))

	// 先存草稿
	res := ex.Execute(context.Background(), TaskScript, llm.ToolCall{
		ID: "1", Function: llm.ToolCallFunc{Name: "save_script_draft",
			Arguments: `{"name":"new.lua","source":"function on_frame(raw)\n  return { v = 2 }\nend\n"}`},
	})
	if !res.OK {
		t.Fatalf("save draft failed: %+v", res)
	}

	res = ex.Execute(context.Background(), TaskScript, llm.ToolCall{
		ID: "2", Function: llm.ToolCallFunc{Name: "request_user_confirm",
			Arguments: `{"title":"应用脚本","description":"将替换 main.lua"}`},
	})
	if !res.OK {
		t.Fatalf("confirm failed: %+v", res)
	}
	if got.Kind != "script_draft" || got.Content == "" || !strings.Contains(got.Diff, "+  return { v = 2 }") {
		t.Fatalf("confirm request missing draft/diff: %+v", got)
	}
}

func TestSampleStoreReadsHexAndBinary(t *testing.T) {
	h := newFakeHost(t)
	store := NewSampleStore(h.dir)

	raw := []byte{0xAA, 0x06, 0x01, 0x02, 0x03, 0x04}
	if err := os.WriteFile(filepath.Join(h.dir, ".seriallens", "samples", "b.bin"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	list, err := store.List(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("samples = %d, want 2 (%+v)", len(list), list)
	}

	got, err := store.Read("a.hex")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(raw) {
		t.Fatalf("hex parse = %X, want %X", got, raw)
	}
	got, err = store.Read("b.bin")
	if err != nil || string(got) != string(raw) {
		t.Fatalf("bin read = %X, %v", got, err)
	}

	if _, err := store.Read("../project.json"); err == nil {
		t.Fatal("path traversal must be rejected")
	}
}

func TestRunLuaOnSamplesUsesRecentSamples(t *testing.T) {
	h := newFakeHost(t)
	ex := NewExecutor(h, nil)

	res := ex.Execute(context.Background(), TaskScript, llm.ToolCall{
		ID: "1", Function: llm.ToolCallFunc{Name: "run_lua_on_samples",
			Arguments: `{"source":"function on_frame(raw) return {} end"}`},
	})
	if !res.OK {
		t.Fatalf("run failed: %+v", res)
	}
	m, _ := res.Result.(map[string]any)
	if m["samples"].(int) != 1 {
		t.Fatalf("expected 1 sample, got %+v", m)
	}
	out := m["results"].([]RunOutcome)
	if out[0].SampleID != "a.hex" {
		t.Fatalf("sample id = %q", out[0].SampleID)
	}
}

func TestSessionAppendListAndTrim(t *testing.T) {
	dir := t.TempDir()
	sess, err := NewSession(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	sess.Append(SessionEvent{Type: EvSessionMeta, Model: "m", Title: "温度解码"})
	sess.Append(SessionEvent{Type: EvSessionMessage, Role: "user", Content: "帮我解析"})
	sess.Append(SessionEvent{Type: EvSessionToolCall, CallID: "c1", ToolName: "compile_lua", Args: map[string]any{"x": 1}})
	sess.Append(SessionEvent{Type: EvSessionMessage, Role: "assistant", Content: "好"})

	evs, err := LoadEvents(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 4 || evs[1].Role != "user" {
		t.Fatalf("events = %+v", evs)
	}

	list, err := ListSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Title != "温度解码" || list[0].Turns != 1 {
		t.Fatalf("session list = %+v", list)
	}
}

// TestRunnerToolLoop 用假 SSE 服务验证：tool_calls → 执行 → 回喂 → 最终回答。
func TestRunnerToolLoop(t *testing.T) {
	var round int
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		round++
		cur := round
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)

		write := func(v any) {
			raw, _ := json.Marshal(v)
			fmt.Fprintf(w, "data: %s\n\n", raw)
			if fl != nil {
				fl.Flush()
			}
		}

		if cur == 1 {
			// 第一轮：要求调用 compile_lua
			write(map[string]any{
				"choices": []any{map[string]any{
					"index": 0,
					"delta": map[string]any{"tool_calls": []any{map[string]any{
						"index": 0, "id": "call_1",
						"function": map[string]any{"name": "compile_lua", "arguments": `{"source":"function on_frame(raw) return {} end"}`},
					}}},
					"finish_reason": "tool_calls",
				}},
			})
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}

		// 第二轮：最终回答
		write(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "脚本已准备好"}, "finish_reason": "stop"}}})
		write(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}})
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	h := newFakeHost(t)
	sess, _ := NewSession(h.dir, "")
	runner := &Runner{
		Client:    llm.NewClientRaw(srv.URL+"/v1", "k", "mock"),
		Model:     "mock",
		Exec:      NewExecutor(h, nil),
		Session:   sess,
		RunID:     "run-1",
		MaxRounds: 4,
	}
	msgs := runner.BuildMessages(TaskScript, Context{ProjectName: "demo", ActiveScript: "main.lua", ScriptSource: h.scripts["main.lua"]}, []llm.Message{
		{Role: "user", Content: "写个解码器"},
	})

	res, err := runner.Run(context.Background(), TaskScript, msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 2 || res.ToolCalls != 1 {
		t.Fatalf("rounds=%d toolcalls=%d", res.Rounds, res.ToolCalls)
	}
	if !strings.Contains(res.Content, "脚本已准备好") {
		t.Fatalf("content = %q", res.Content)
	}

	evs, _ := LoadEvents(sess.Path())
	var sawCall, sawResult bool
	for _, e := range evs {
		if e.Type == EvSessionToolCall && e.ToolName == "compile_lua" {
			sawCall = true
		}
		if e.Type == EvSessionToolRes && e.OK != nil && *e.OK {
			sawResult = true
		}
	}
	if !sawCall || !sawResult {
		t.Fatalf("session missing tool events: %+v", evs)
	}
}

func TestUnifiedDiff(t *testing.T) {
	d := unifiedDiff("main.lua", "a\nb\nc\n", "new.lua", "a\nB\nc\n")
	if !strings.Contains(d, "-b") || !strings.Contains(d, "+B") || !strings.Contains(d, " a") {
		t.Fatalf("diff = %q", d)
	}
}
