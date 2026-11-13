package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"seriallens/internal/llm"
)

// 工具调用循环的硬性上限：超过就停，避免模型自己绕圈烧 token。
const (
	DefaultMaxRounds = 8
	maxToolPayload   = 4000 // 单条工具结果回喂上限（字符）
)

// Context 每次请求注入的工程上下文（PRD §4.5.1，按优先级注入）。
type Context struct {
	ProjectName  string
	ContextHints string
	ActiveScript string
	ScriptSource string
	Scripts      []string
	Charts       map[string]any
	Samples      []SampleMeta
	// SystemExtra 用户在设置里填的「工程偏好」（llm.Settings.SystemExtra）。
	SystemExtra string
}

// Runner 工具调用循环（流式 + tool_calls 回喂）。
type Runner struct {
	Client      *llm.Client
	Model       string
	MaxTokens   int
	Temperature *float64
	MaxRounds   int
	Exec        *Executor
	Session     *Session
	Emit        func(event string, payload map[string]any)
	RunID       string
}

// RunResult 一轮用户输入跑完后的汇总。
type RunResult struct {
	Content    string
	Reasoning  string
	Rounds     int
	ToolCalls  int
	Canceled   bool
	Usage      *llm.Usage
	ElapsedMS  int64
	FirstMS    int64
	FinishText string
}

// Run 执行「用户输入 →（工具调用 → 回喂）* → 最终回答」。
// onText 用于把增量文本推给 UI；返回的 RunResult.Content 是最后一轮的完整正文。
func (r *Runner) Run(ctx context.Context, task Task, history []llm.Message, onText func(content, reasoning string) error) (*RunResult, error) {
	if r.Client == nil {
		return nil, fmt.Errorf("llm client is nil")
	}
	if r.MaxRounds <= 0 {
		r.MaxRounds = DefaultMaxRounds
	}
	start := time.Now()
	res := &RunResult{}

	msgs := append([]llm.Message{}, history...)
	tools := NewRegistry().Defs(task)

	var finalContent, finalReasoning strings.Builder

	for round := 1; round <= r.MaxRounds; round++ {
		res.Rounds = round
		var roundContent, roundReasoning strings.Builder

		req := llm.ChatRequest{
			Model:       r.Model,
			Messages:    msgs,
			Temperature: r.Temperature,
			MaxTokens:   r.MaxTokens,
			Tools:       tools,
		}
		resp, err := r.Client.ChatStream(ctx, req, func(d llm.Delta) error {
			if d.Content != "" {
				roundContent.WriteString(d.Content)
			}
			if d.Reasoning != "" {
				roundReasoning.WriteString(d.Reasoning)
			}
			if onText != nil && (d.Content != "" || d.Reasoning != "") {
				return onText(d.Content, d.Reasoning)
			}
			return nil
		})
		if err != nil {
			res.Content = finalContent.String() + roundContent.String()
			res.Reasoning = finalReasoning.String() + roundReasoning.String()
			res.ElapsedMS = time.Since(start).Milliseconds()
			res.Canceled = ctx.Err() != nil
			return res, err
		}

		msg := resp.Choices[0].Message
		content := roundContent.String()
		if content == "" {
			content = msg.Content
		}
		finalContent.WriteString(content)
		finalReasoning.WriteString(roundReasoning.String())
		if resp.Usage != nil {
			res.Usage = resp.Usage
		}
		if resp.FirstDeltaMS > 0 && res.FirstMS == 0 {
			res.FirstMS = resp.FirstDeltaMS
		}

		if len(msg.ToolCalls) == 0 {
			res.FinishText = resp.FinishReason()
			break
		}

		// 助手消息必须原样回传（含 tool_calls），否则供应商报 400
		msgs = append(msgs, llm.Message{
			Role:      "assistant",
			Content:   content,
			ToolCalls: msg.ToolCalls,
		})
		if r.Session != nil {
			r.Session.Append(SessionEvent{Type: EvSessionMessage, Role: "assistant", Content: content})
		}

		for _, call := range msg.ToolCalls {
			if ctx.Err() != nil {
				res.Canceled = true
				break
			}
			r.emit("ai:tool_call", map[string]any{
				"id":      r.RunID,
				"call_id": call.ID,
				"name":    call.Function.Name,
				"args":    json.RawMessage(call.Function.Arguments),
				"round":   round,
			})
			if r.Session != nil {
				r.Session.Append(SessionEvent{
					Type: EvSessionToolCall, CallID: call.ID, ToolName: call.Function.Name,
					Args: decodeArgs(call.Function.Arguments),
				})
			}

			result := r.Exec.Execute(ctx, task, call)
			res.ToolCalls++

			ok := result.OK
			r.emit("ai:tool_result", map[string]any{
				"id":          r.RunID,
				"call_id":     call.ID,
				"name":        result.Name,
				"ok":          result.OK,
				"duration_ms": result.DurationMS,
				"result":      truncateAny(result.Result, 2000),
				"error":       result.Error,
			})
			if r.Session != nil {
				r.Session.Append(SessionEvent{
					Type: EvSessionToolRes, CallID: call.ID, ToolName: result.Name,
					OK: &ok, Duration: result.DurationMS,
					Result: truncateAny(pickResult(result), 4000),
				})
			}

			msgs = append(msgs, llm.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Name:       result.Name,
				Content:    toolPayload(result),
			})
			if ctx.Err() != nil {
				res.Canceled = true
				break
			}
		}
		if res.Canceled {
			res.Content = finalContent.String()
			res.Reasoning = finalReasoning.String()
			res.ElapsedMS = time.Since(start).Milliseconds()
			return res, ctx.Err()
		}
	}

	res.Content = finalContent.String()
	res.Reasoning = finalReasoning.String()
	res.ElapsedMS = time.Since(start).Milliseconds()
	if res.FinishText == "" {
		res.FinishText = "max_rounds"
	}
	return res, nil
}

// BuildMessages 组装 system + 历史（system 每次现拼，脚本内容随时在变）。
func (r *Runner) BuildMessages(task Task, ctxInfo Context, history []llm.Message) []llm.Message {
	out := make([]llm.Message, 0, len(history)+1)
	out = append(out, llm.Message{Role: "system", Content: SystemPrompt(task, ctxInfo)})
	for _, m := range history {
		// 空 assistant 消息（正文与 tool_calls 都为空）会被供应商拒掉，直接丢弃
		if m.Role == "assistant" && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
			continue
		}
		out = append(out, m)
	}
	return out
}

func (r *Runner) emit(event string, payload map[string]any) {
	if r.Emit != nil {
		r.Emit(event, payload)
	}
}

// toolPayload 回喂给模型的工具结果（结构化 + 截断）。
func toolPayload(res ToolResult) string {
	body := map[string]any{"ok": res.OK}
	if res.Error != "" {
		body["error"] = res.Error
	}
	if res.Result != nil {
		body["result"] = truncateAny(res.Result, maxToolPayload)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return `{"ok":false,"error":"marshal failed"}`
	}
	return string(raw)
}

func pickResult(res ToolResult) any {
	if res.OK {
		return res.Result
	}
	return map[string]any{"error": res.Error}
}

// truncateAny 对任意 JSON 值做「序列化后截断」，保持结构可控。
func truncateAny(v any, max int) any {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	if len(raw) <= max {
		return v
	}
	return string(raw[:max]) + "…（已截断）"
}

func decodeArgs(s string) map[string]any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return map[string]any{"_raw": s}
	}
	return m
}
