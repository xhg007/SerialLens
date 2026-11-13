package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseServer 模拟 OpenAI 兼容端点的流式响应。
func sseServer(t *testing.T, chunks []string, gotReq *ChatRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if gotReq != nil {
			_ = json.NewDecoder(r.Body).Decode(gotReq)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, c := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", c)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(5 * time.Millisecond)
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func TestChatStreamAggregatesContentReasoningAndUsage(t *testing.T) {
	chunks := []string{
		`{"id":"c1","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"先看帧头"}}]}`,
		`{"id":"c1","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"content":"function on_frame"}}]}`,
		`{"id":"c1","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"content":"(raw) end"}}]}`,
		`{"id":"c1","model":"deepseek-reasoner","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`,
	}
	var gotReq ChatRequest
	srv := sseServer(t, chunks, &gotReq)
	defer srv.Close()

	c := NewClientRaw(srv.URL, "test-key", "deepseek-reasoner")
	temp := 0.2
	var deltas []string
	resp, err := c.ChatStream(context.Background(), ChatRequest{
		Model:       "deepseek-reasoner",
		Messages:    []Message{{Role: "user", Content: "写个脚本"}},
		Temperature: &temp,
	}, func(d Delta) error {
		if d.Content != "" {
			deltas = append(deltas, d.Content)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if resp.Content() != "function on_frame(raw) end" {
		t.Fatalf("content = %q", resp.Content())
	}
	if resp.Reasoning() != "先看帧头" {
		t.Fatalf("reasoning = %q", resp.Reasoning())
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 18 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if len(deltas) != 2 {
		t.Fatalf("deltas = %v", deltas)
	}
	if !gotReq.Stream || gotReq.StreamOptions == nil || !gotReq.StreamOptions.IncludeUsage {
		t.Fatalf("stream request flags wrong: %+v", gotReq)
	}
	if gotReq.MaxTokens != 0 || gotReq.Messages[0].Role != "user" {
		t.Fatalf("request body = %+v", gotReq)
	}
}

func TestChatStreamAssemblesToolCalls(t *testing.T) {
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"compile_lua","arguments":"{\"src\":"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"return 1\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	srv := sseServer(t, chunks, nil)
	defer srv.Close()

	c := NewClientRaw(srv.URL, "test-key", "deepseek-chat")
	resp, err := c.ChatStream(context.Background(), ChatRequest{
		Model:    "deepseek-chat",
		Messages: []Message{{Role: "user", Content: "x"}},
		Tools: []Tool{{
			Type:     "function",
			Function: ToolDef{Name: "compile_lua", Description: "编译检查", Parameters: map[string]any{"type": "object"}},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", resp.Choices[0].Message.ToolCalls)
	}
	tc := resp.Choices[0].Message.ToolCalls[0]
	if tc.ID != "call_1" || tc.Function.Name != "compile_lua" {
		t.Fatalf("tool call = %+v", tc)
	}
	if tc.Function.Arguments != `{"src":"return 1"}` {
		t.Fatalf("arguments not reassembled: %q", tc.Function.Arguments)
	}
	if resp.FinishReason() != "tool_calls" {
		t.Fatalf("finish_reason = %q", resp.FinishReason())
	}
}

// 自建端点（vLLM/LM Studio 老版本）不认 stream_options，应自动去掉重试。
func TestChatStreamRetriesWithoutStreamOptionsOn400(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.StreamOptions != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unknown field stream_options"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewClientRaw(srv.URL, "k", "m")
	resp, err := c.ChatStream(context.Background(), ChatRequest{Model: "m"}, nil)
	if err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if resp.Content() != "ok" {
		t.Fatalf("content = %q", resp.Content())
	}
}

func TestChatNonStreamAndAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model == "bad-model" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"model not found","code":"model_not_found"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    "x",
			"model": req.Model,
			"choices": []any{
				map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "你好"}, "finish_reason": "stop"},
			},
			"usage": map[string]any{"total_tokens": 3},
		})
	}))
	defer srv.Close()

	c := NewClientRaw(srv.URL, "k", "good")
	resp, err := c.Chat(context.Background(), ChatRequest{Model: "good", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content() != "你好" || resp.Usage.TotalTokens != 3 {
		t.Fatalf("resp = %+v", resp)
	}

	_, err = c.Chat(context.Background(), ChatRequest{Model: "bad-model", Messages: []Message{{Role: "user", Content: "hi"}}})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 404 || apiErr.Code != "model_not_found" {
		t.Fatalf("err = %+v", err)
	}
	if !strings.Contains(FriendlyError(err), "端点或模型不存在") {
		t.Fatalf("friendly message = %q", FriendlyError(err))
	}
}

func TestFriendlyErrorMappings(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{401, "API Key 无效"},
		{402, "余额不足"},
		{429, "限流"},
		{500, "服务端错误"},
	}
	for _, c := range cases {
		msg := FriendlyError(&APIError{Status: c.status, Message: "x"})
		if !strings.Contains(msg, c.want) {
			t.Errorf("status %d -> %q, want contains %q", c.status, msg, c.want)
		}
	}
	if got := FriendlyError(context.Canceled); got != "已停止" {
		t.Errorf("canceled -> %q", got)
	}
}

func TestPingUsesModelsEndpointWithoutTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-chat"},{"id":"deepseek-reasoner"}]}`))
			return
		}
		t.Errorf("should not call %s when /models works", r.URL.Path)
	}))
	defer srv.Close()

	c := NewClientRaw(srv.URL, "k", "deepseek-chat")
	res, err := c.Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Via != "models" || len(res.Models) != 2 {
		t.Fatalf("ping = %+v", res)
	}
}

func TestPingFallsBackToChatWhenModelsMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"model":"m1","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	c := NewClientRaw(srv.URL, "k", "m1")
	res, err := c.Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Via != "chat" || res.Reply != "pong" {
		t.Fatalf("ping = %+v", res)
	}
}

// 用户点「停止」= ctx 取消，必须立刻返回且不 panic。
func TestChatStreamHonorsCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 100; i++ {
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := NewClientRaw(srv.URL, "k", "m")
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := c.ChatStream(ctx, ChatRequest{Model: "m"}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
