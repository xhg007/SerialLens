// Command mockllm 本地 OpenAI 兼容假服务（仅开发/验收用，不进发布包）。
//
// 用途：没有 API Key 时验证 AI 面板的完整链路——设置保存、连通性测试、
// 流式增量渲染、停止、多轮上下文，全都走真实 HTTP/SSE，而不是打桩。
//
//	go run ./cmd/mockllm -addr 127.0.0.1:11435
//
// 然后在应用里选「自定义」，Base URL 填 http://127.0.0.1:11435/v1，Key 随便填。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:11435", "监听地址")
	delay := flag.Duration("delay", 18*time.Millisecond, "每个 token 的延迟（模拟真实网络）")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("GET /v1/models")
		writeJSON(w, map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "mock-coder", "object": "model"},
				{"id": "mock-reasoner", "object": "model"},
			},
		})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var lastUser string
		for _, m := range req.Messages {
			if m.Role == "user" {
				lastUser = m.Content
			}
		}
		reply := buildReply(lastUser)
		log.Printf("POST /v1/chat/completions model=%s stream=%v messages=%d user=%q",
			req.Model, req.Stream, len(req.Messages), truncate(lastUser, 40))

		if !req.Stream {
			writeJSON(w, map[string]any{
				"id": "mock-1", "object": "chat.completion", "model": req.Model,
				"choices": []any{map[string]any{
					"index":         0,
					"message":       map[string]any{"role": "assistant", "content": reply},
					"finish_reason": "stop",
				}},
				"usage": map[string]any{"prompt_tokens": 42, "completion_tokens": 128, "total_tokens": 170},
			})
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		send := func(v any) bool {
			raw, _ := json.Marshal(v)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
				return false
			}
			if flusher != nil {
				flusher.Flush()
			}
			return true
		}

		for _, ch := range splitRunes(reply) {
			select {
			case <-r.Context().Done():
				log.Printf("client canceled")
				return
			default:
			}
			if !send(map[string]any{
				"id": "mock-1", "model": req.Model,
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": ch}}},
			}) {
				return
			}
			time.Sleep(*delay)
		}
		send(map[string]any{
			"id": "mock-1", "model": req.Model,
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 42, "completion_tokens": 128, "total_tokens": 170},
		})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	})

	log.Printf("mockllm listening on http://%s/v1 （Base URL 填这个地址）", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

// buildReply 返回一段带 ``` 代码块的固定回答，用来验证前端代码块渲染/复制。
func buildReply(user string) string {
	if user == "" {
		user = "（空）"
	}
	return "收到：" + truncate(user, 30) + "\n\n" +
		"下面是一个按 64 字节定长帧解析的示例：\n\n" +
		"```lua\n" +
		"function detect_frame(buf)\n" +
		"    if #buf < 64 then return 0 end\n" +
		"    return 64\n" +
		"end\n\n" +
		"function on_frame(raw)\n" +
		"    local fields = { seq = struct_uint32_le(raw, 0) }\n" +
		"    for i = 1, 4 do\n" +
		"        local v = struct_uint16_be(raw, i * 2)\n" +
		"        fields[\"ch\" .. i] = v\n" +
		"        emit_chart(\"ch\" .. i, v)\n" +
		"    end\n" +
		"    return fields\n" +
		"end\n" +
		"```\n\n" +
		"这是本地 mock 服务的固定回答，用于验证流式渲染与代码块。"
}

func splitRunes(s string) []string {
	out := make([]string, 0, len(s))
	for _, r := range s {
		out = append(out, string(r))
	}
	return out
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
