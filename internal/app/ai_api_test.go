package app

import (
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

	"seriallens/internal/agent"
	"seriallens/internal/events"
	"seriallens/internal/llm"
	"seriallens/internal/project"
)

// ---------- mock OpenAI 兼容端点 ----------

type mockLLM struct {
	mu      sync.Mutex
	reqs    []llm.ChatRequest
	answer  string
	block   chan struct{} // 非 nil 时先阻塞，用于「生成中」用例
	perTok  time.Duration
	models  []string
	noModel bool
}

func (m *mockLLM) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			if m.noModel {
				http.NotFound(w, r)
				return
			}
			ids := m.models
			if len(ids) == 0 {
				ids = []string{"mock-1"}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": ids[0]}},
			})
			return
		}
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var req llm.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		m.mu.Lock()
		m.reqs = append(m.reqs, req)
		answer, block, perTok := m.answer, m.block, m.perTok
		m.mu.Unlock()

		if block != nil {
			select {
			case <-block:
			case <-r.Context().Done():
				return
			}
		}

		// 非流式调用（Ping 的兜底路径）要返回 JSON，而不是 SSE
		if !req.Stream {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "x", "model": "mock-1",
				"choices": []any{map[string]any{
					"index": 0, "message": map[string]any{"role": "assistant", "content": answer}, "finish_reason": "stop",
				}},
			})
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, ch := range strings.Split(answer, "") {
			_, _ = fmt.Fprintf(w, "data: {\"id\":\"x\",\"model\":\"mock-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", ch)
			if flusher != nil {
				flusher.Flush()
			}
			if perTok > 0 {
				time.Sleep(perTok)
			}
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

func (m *mockLLM) requests() []llm.ChatRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]llm.ChatRequest(nil), m.reqs...)
}

// ---------- 事件收集 ----------

type aiRecorder struct {
	mu     sync.Mutex
	events []string
	dones  []map[string]any
	errs   []string
	deltas []string
}

func (r *aiRecorder) emit(name string, payload map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, name)
	switch name {
	case events.EvAIDone:
		r.dones = append(r.dones, payload)
	case events.EvAIError:
		if s, ok := payload["message"].(string); ok {
			r.errs = append(r.errs, s)
		}
	case events.EvAIDelta:
		if s, ok := payload["text"].(string); ok && s != "" {
			r.deltas = append(r.deltas, s)
		}
	}
}

func (r *aiRecorder) waitDone(t *testing.T, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		if len(r.dones) > 0 {
			d := r.dones[0]
			r.mu.Unlock()
			return d
		}
		r.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	r.mu.Lock()
	ev := append([]string(nil), r.events...)
	errs := append([]string(nil), r.errs...)
	r.mu.Unlock()
	t.Fatalf("no ai:done within %v; events=%v errors=%v", timeout, ev, errs)
	return nil
}

// ---------- 夹具 ----------

func newAIFixture(t *testing.T) (*AIAPI, *mockLLM, *aiRecorder) {
	t.Helper()
	mock := &mockLLM{answer: "你好呀"}
	srv := httptest.NewServer(mock.handler(t))
	t.Cleanup(srv.Close)

	rec := &aiRecorder{}
	s := &Services{}
	s.emit = rec.emit
	ai := NewAIService(s, t.TempDir())
	if _, err := ai.SaveAISettings(AISettingsInput{
		Provider: "custom", BaseURL: srv.URL, Model: "mock-1", APIKey: "sk-test",
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	return ai, mock, rec
}

// ---------- 用例 ----------

// 多轮流式对话：首轮内容正确、增量为流式、历史进下一轮。
func TestAIMultiTurnStreaming(t *testing.T) {
	ai, mock, rec := newAIFixture(t)

	id, err := ai.SendAIMessage("写一个 on_frame")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if id == "" {
		t.Fatal("empty run id")
	}
	done := rec.waitDone(t, 5*time.Second)
	if done["content"] != "你好呀" {
		t.Fatalf("content = %v", done["content"])
	}
	if done["canceled"] != false {
		t.Fatalf("canceled = %v", done["canceled"])
	}
	usage, _ := done["usage"].(map[string]any)
	if usage["total_tokens"] != 15 {
		t.Fatalf("usage = %v", usage)
	}

	rec.mu.Lock()
	deltaCount := len(rec.deltas)
	rec.mu.Unlock()
	if deltaCount == 0 {
		t.Fatal("no ai:delta emitted")
	}

	// 第二轮：历史必须带上下文
	if _, err := ai.SendAIMessage("再解释一下"); err != nil {
		t.Fatalf("second send: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(mock.requests()) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	reqs := mock.requests()
	if len(reqs) < 2 {
		t.Fatalf("requests = %d", len(reqs))
	}
	second := reqs[1].Messages
	var roles []string
	for _, m := range second {
		roles = append(roles, m.Role)
	}
	joined := strings.Join(roles, ",")
	if !strings.Contains(joined, "system,user,assistant,user") {
		t.Fatalf("history roles = %v", roles)
	}
	if !strings.Contains(second[0].Content, "SerialLens") || !strings.Contains(second[0].Content, "on_frame") {
		t.Fatal("system prompt should inject identity + API docs")
	}
	if conv := ai.GetAIConversation(); len(conv) != 4 {
		t.Fatalf("conversation = %d messages", len(conv))
	}
}

// 生成中再发一条必须被拒绝（避免同一会话两条并发流互相覆盖）。
func TestAISendRejectsWhileRunning(t *testing.T) {
	ai, mock, rec := newAIFixture(t)
	release := make(chan struct{})
	mock.mu.Lock()
	mock.block = release
	mock.mu.Unlock()

	if _, err := ai.SendAIMessage("第一条"); err != nil {
		t.Fatal(err)
	}
	if _, err := ai.SendAIMessage("第二条"); err == nil {
		t.Fatal("second send should be rejected while running")
	}
	close(release)
	rec.waitDone(t, 5*time.Second)

	if _, err := ai.SendAIMessage("第三条"); err != nil {
		t.Fatalf("send after finish should work: %v", err)
	}
}

// 停止：已输出内容保留，done 标记 canceled。
func TestAICancelKeepsPartialContent(t *testing.T) {
	ai, mock, rec := newAIFixture(t)
	mock.mu.Lock()
	mock.answer = strings.Repeat("字", 80)
	mock.perTok = 15 * time.Millisecond
	mock.mu.Unlock()

	if _, err := ai.SendAIMessage("长回答"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if err := ai.CancelAI(); err != nil {
		t.Fatal(err)
	}
	done := rec.waitDone(t, 5*time.Second)
	if done["canceled"] != true {
		t.Fatalf("canceled = %v", done["canceled"])
	}
	content, _ := done["content"].(string)
	if content == "" {
		t.Fatal("partial content should be kept")
	}
	if !strings.Contains(content, "字") {
		t.Fatalf("partial content = %q", content)
	}
	if got := ai.GetAIStatus()["running"]; got != false {
		t.Fatalf("running after cancel = %v", got)
	}
}

func TestAISettingsMaskedAndPersisted(t *testing.T) {
	ai, _, _ := newAIFixture(t)
	view := ai.GetAISettings()
	if view["has_key"] != true {
		t.Fatalf("has_key = %v", view["has_key"])
	}
	if _, ok := view["api_key"]; ok {
		t.Fatal("api_key must never be returned")
	}
	if view["model"] != "mock-1" || view["ready"] != true {
		t.Fatalf("view = %+v", view)
	}

	// 重新加载（同一 home）应仍是已配置状态
	ai2 := NewAIService(ai.s, ai.store.Dir())
	if v := ai2.GetAISettings(); v["has_key"] != true || v["model"] != "mock-1" {
		t.Fatalf("persisted view = %+v", v)
	}

	// 清除 Key
	if _, err := ai.SaveAISettings(AISettingsInput{ClearKey: true}); err != nil {
		t.Fatal(err)
	}
	if v := ai.GetAISettings(); v["has_key"] != false || v["ready"] != false {
		t.Fatalf("after clear: %+v", v)
	}
}

func TestAISendRejectsWhenNotReady(t *testing.T) {
	rec := &aiRecorder{}
	s := &Services{}
	s.emit = rec.emit
	ai := NewAIService(s, t.TempDir())
	if _, err := ai.SendAIMessage("hi"); err == nil {
		t.Fatal("should reject without provider config")
	}
}

func TestAIConnectionPing(t *testing.T) {
	ai, mock, _ := newAIFixture(t)
	mock.mu.Lock()
	mock.models = []string{"mock-1"}
	mock.mu.Unlock()

	res, err := ai.TestAIConnection(AISettingsInput{})
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if res["ok"] != true || res["via"] != "models" {
		t.Fatalf("ping result = %+v", res)
	}

	// 端点不存在 /models 时退化成一次短对话
	mock.mu.Lock()
	mock.noModel = true
	mock.mu.Unlock()
	res, err = ai.TestAIConnection(AISettingsInput{})
	if err != nil {
		t.Fatalf("ping fallback: %v", err)
	}
	if res["via"] != "chat" {
		t.Fatalf("fallback via = %v", res["via"])
	}
}

func TestAIConnectionReportsFriendlyError(t *testing.T) {
	rec := &aiRecorder{}
	s := &Services{}
	s.emit = rec.emit
	ai := NewAIService(s, t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	_, err := ai.TestAIConnection(AISettingsInput{
		Provider: "custom", BaseURL: srv.URL, Model: "m", APIKey: "bad",
	})
	if err == nil || !strings.Contains(err.Error(), "API Key 无效") {
		t.Fatalf("err = %v", err)
	}
}

func TestAIClearConversation(t *testing.T) {
	ai, _, rec := newAIFixture(t)
	if _, err := ai.SendAIMessage("hi"); err != nil {
		t.Fatal(err)
	}
	rec.waitDone(t, 5*time.Second)
	if len(ai.GetAIConversation()) == 0 {
		t.Fatal("conversation should have messages")
	}
	if err := ai.ClearAIConversation(); err != nil {
		t.Fatal(err)
	}
	if n := len(ai.GetAIConversation()); n != 0 {
		t.Fatalf("conversation after clear = %d", n)
	}
}

// 工程上下文注入：system prompt 要带当前脚本内容（PRD §4.5.1）。
func TestAISystemPromptInjectsCurrentScript(t *testing.T) {
	s, _, _ := newWiredServices(t)
	rec := &aiRecorder{}
	s.emit = rec.emit

	dir := t.TempDir()
	meta, err := project.Scaffold(dir, "ai-ctx", "")
	if err != nil {
		t.Fatal(err)
	}
	marker := "-- MARKER-abc123 小端模式"
	if err := os.WriteFile(filepath.Join(dir, "scripts", meta.ActiveScript), []byte(marker+"\nfunction on_frame(raw) return {} end\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Project.svc.Open(dir); err != nil {
		t.Fatal(err)
	}

	mock := &mockLLM{answer: "ok"}
	srv := httptest.NewServer(mock.handler(t))
	defer srv.Close()

	ai := NewAIService(s, t.TempDir())
	if _, err := ai.SaveAISettings(AISettingsInput{
		Provider: "custom", BaseURL: srv.URL, Model: "mock-1", APIKey: "k",
		SystemExtra: "优先使用小端解析",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ai.SendAIMessage("看看我的脚本"); err != nil {
		t.Fatal(err)
	}
	rec.waitDone(t, 5*time.Second)

	reqs := mock.requests()
	if len(reqs) == 0 {
		t.Fatal("no request captured")
	}
	sys := reqs[0].Messages[0]
	if sys.Role != "system" {
		t.Fatalf("first message role = %s", sys.Role)
	}
	if !strings.Contains(sys.Content, marker) {
		t.Fatal("system prompt should include current script content")
	}
	if !strings.Contains(sys.Content, "优先使用小端解析") {
		t.Fatal("system prompt should include system_extra")
	}
}

// user_action 落 JSONL（PRD §4.6 回溯）：确认卡片的用户决定必须留痕。
func TestAIRecordUserAction(t *testing.T) {
	ai, _, _ := newAIFixture(t)
	// 直接挂一个临时目录会话（RecordUserAction 只依赖 a.sess）
	sess, err := agent.NewSession(t.TempDir(), "test_act")
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	ai.mu.Lock()
	ai.sess = sess
	ai.mu.Unlock()

	ai.RecordUserAction("apply_script", "main.lua")
	ai.RecordUserAction("reject_confirm", "应用脚本")

	// 无会话/空 action 不 panic、不写
	ai2 := &AIAPI{}
	ai2.RecordUserAction("apply_script", "x") // sess == nil
	sess2, _ := agent.NewSession(t.TempDir(), "test_act2")
	ai3 := &AIAPI{sess: sess2}
	ai3.RecordUserAction("", "") // action == ""

	events, err := agent.LoadEvents(sess.Path())
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	var acts []agent.SessionEvent
	for _, ev := range events {
		if ev.Type == agent.EvSessionUserAct {
			acts = append(acts, ev)
		}
	}
	if len(acts) != 2 {
		t.Fatalf("want 2 user_action events, got %d", len(acts))
	}
	if acts[0].Action != "apply_script" || acts[0].Content != "main.lua" {
		t.Fatalf("first event = %+v", acts[0])
	}
	if acts[1].Action != "reject_confirm" || acts[1].Content != "应用脚本" {
		t.Fatalf("second event = %+v", acts[1])
	}

	evs2, _ := agent.LoadEvents(sess2.Path())
	for _, ev := range evs2 {
		if ev.Type == agent.EvSessionUserAct {
			t.Fatal("empty action must not be recorded")
		}
	}
}
