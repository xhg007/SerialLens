package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Message 一条对话消息（OpenAI chat 格式）。
type Message struct {
	Role    string `json:"role"` // system | user | assistant | tool
	Content string `json:"content,omitempty"`
	// ReasoningContent 是 DeepSeek-R1 / Qwen-QwQ 等「思维链」模型的推理输出；
	// 只在响应里出现，回传历史时不带（多数供应商会拒绝该字段）。
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	Name             string     `json:"name,omitempty"`

	// CreatedAt 本地时间戳（UI 排序用，不参与请求）。
	CreatedAt int64 `json:"-"`
}

// ToolCall 模型请求调用某个工具。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolCallFunc `json:"function"`
}

// ToolCallFunc 工具名与 JSON 参数（流式时是分片拼接的）。
type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool 工具声明（M5 第二阶段才会真正下发，先留好协议）。
type Tool struct {
	Type     string  `json:"type"`
	Function ToolDef `json:"function"`
}

// ToolDef 工具定义。
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// StreamOptions 让供应商在流末尾补一个 usage 分片（DeepSeek/Qwen/Moonshot 均支持）。
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// ChatRequest /chat/completions 请求体。
type ChatRequest struct {
	Model         string         `json:"model"`
	Messages      []Message      `json:"messages"`
	Temperature   *float64       `json:"temperature,omitempty"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
	Stream        bool           `json:"stream,omitempty"`
	StreamOptions *StreamOptions `json:"stream_options,omitempty"`
	Tools         []Tool         `json:"tools,omitempty"`
	ToolChoice    any            `json:"tool_choice,omitempty"`
}

// Usage token 统计。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Choice 一个候选结果。
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	Delta        Message `json:"delta"`
	FinishReason string  `json:"finish_reason"`
}

// ChatResponse 汇总后的响应（流式与非流式统一结构）。
type ChatResponse struct {
	ID      string   `json:"id"`
	Model   string   `json:"model"`
	Created int64    `json:"created"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage"`

	// 本地统计
	ElapsedMS    int64 `json:"-"`
	FirstDeltaMS int64 `json:"-"`
}

// Content 首条候选的正文。
func (r *ChatResponse) Content() string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.Content
}

// Reasoning 首条候选的思维链。
func (r *ChatResponse) Reasoning() string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.ReasoningContent
}

// FinishReason 首条候选的结束原因。
func (r *ChatResponse) FinishReason() string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].FinishReason
}

// Delta 流式增量。
type Delta struct {
	Content      string
	Reasoning    string
	ToolCalls    []ToolCallDelta
	FinishReason string
}

// ToolCallDelta 工具调用分片（按 Index 合并）。
type ToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

// APIError 供应商返回的错误（保留原始 body 便于排查）。
type APIError struct {
	Status  int
	Message string
	Type    string
	Code    string
	Body    string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("llm: %d %s", e.Status, e.Message)
	}
	return fmt.Sprintf("llm: http %d", e.Status)
}

// Client OpenAI 兼容客户端（base_url 可指向 DeepSeek / DashScope / Moonshot / Ollama）。
type Client struct {
	baseURL string
	apiKey  string
	model   string
	hc      *http.Client
}

// NewClient 从设置构造客户端（baseURL 末尾斜杠会被裁掉）。
func NewClient(s Settings) *Client {
	r := s.Resolved()
	return NewClientRaw(r.BaseURL, r.APIKey, r.Model)
}

// NewClientRaw 自定义端点构造（测试/试连接用）。
func NewClientRaw(baseURL, apiKey, model string) *Client {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 90 * time.Second,
		IdleConnTimeout:       60 * time.Second,
	}
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:  strings.TrimSpace(apiKey),
		model:   strings.TrimSpace(model),
		hc:      &http.Client{Transport: tr}, // 总超时交给 ctx：流式响应可能很久
	}
}

// Model 当前默认模型。
func (c *Client) Model() string { return c.model }

// WithModel 换模型（同一端点）。
func (c *Client) WithModel(m string) *Client {
	cp := *c
	cp.model = strings.TrimSpace(m)
	return &cp
}

// ListModels GET /models —— 不消耗 token，最适合做「连通性测试」。
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, wrapTransport(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseAPIError(resp.StatusCode, body)
	}
	var doc struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("llm: unexpected /models payload: %w", err)
	}
	out := make([]string, 0, len(doc.Data))
	for _, d := range doc.Data {
		if d.ID != "" {
			out = append(out, d.ID)
		}
	}
	return out, nil
}

// Chat 非流式调用。
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	start := time.Now()
	req.Stream = false
	req.StreamOptions = nil
	body, err := c.post(ctx, req)
	if err != nil {
		return nil, err
	}
	var out ChatResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("llm: decode response: %w (body: %s)", err, truncate(string(body), 300))
	}
	out.ElapsedMS = time.Since(start).Milliseconds()
	if len(out.Choices) == 0 {
		return &out, errors.New("llm: empty choices (供应商返回了空结果)")
	}
	return &out, nil
}

// ChatStream 流式调用；每收到一段增量就回调 onDelta（返回 error 可中断）。
//
// 兼容性：部分自建端点不认识 stream_options 会返回 400，这里自动去掉重试一次。
func (c *Client) ChatStream(ctx context.Context, req ChatRequest, onDelta func(Delta) error) (*ChatResponse, error) {
	resp, err := c.stream(ctx, req, true)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == 400 && strings.Contains(apiErr.Body, "stream_options") {
			resp, err = c.stream(ctx, req, false)
		}
		if err != nil {
			return nil, err
		}
	}
	defer resp.Body.Close()

	start := time.Now()
	out := &ChatResponse{Model: req.Model}
	var content, reasoning strings.Builder
	toolByIndex := map[int]*ToolCall{}
	var firstMS int64

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // 单帧可能很大（含 usage/工具参数）
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Created int64  `json:"created"`
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *Usage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// 单帧坏掉不要整条流失败：跳过并继续（供应商偶发心跳帧）
			continue
		}
		if chunk.ID != "" {
			out.ID = chunk.ID
		}
		if chunk.Model != "" {
			out.Model = chunk.Model
		}
		if chunk.Created != 0 {
			out.Created = chunk.Created
		}
		if chunk.Usage != nil {
			out.Usage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]
		d := Delta{Content: ch.Delta.Content, Reasoning: ch.Delta.ReasoningContent, FinishReason: ch.FinishReason}
		if d.Content != "" {
			content.WriteString(d.Content)
			if firstMS == 0 {
				firstMS = time.Since(start).Milliseconds()
			}
		}
		if d.Reasoning != "" {
			reasoning.WriteString(d.Reasoning)
			if firstMS == 0 {
				firstMS = time.Since(start).Milliseconds()
			}
		}
		for _, tc := range ch.Delta.ToolCalls {
			d.ToolCalls = append(d.ToolCalls, ToolCallDelta{
				Index: tc.Index, ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments,
			})
			cur := toolByIndex[tc.Index]
			if cur == nil {
				cur = &ToolCall{Type: "function"}
				toolByIndex[tc.Index] = cur
			}
			if tc.ID != "" {
				cur.ID = tc.ID
			}
			cur.Function.Name += tc.Function.Name
			cur.Function.Arguments += tc.Function.Arguments
		}
		if onDelta != nil && (d.Content != "" || d.Reasoning != "" || len(d.ToolCalls) > 0 || d.FinishReason != "") {
			if err := onDelta(d); err != nil {
				return out, err
			}
		}
		if ch.FinishReason != "" {
			out.Choices = []Choice{{Index: 0, FinishReason: ch.FinishReason}}
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, wrapTransport(err)
	}

	msg := Message{Role: "assistant", Content: content.String(), ReasoningContent: reasoning.String()}
	if len(toolByIndex) > 0 {
		idxs := make([]int, 0, len(toolByIndex))
		for i := range toolByIndex {
			idxs = append(idxs, i)
		}
		// 按 index 稳定排序，保证工具调用顺序与模型输出一致
		for i := 0; i < len(idxs); i++ {
			for j := i + 1; j < len(idxs); j++ {
				if idxs[j] < idxs[i] {
					idxs[i], idxs[j] = idxs[j], idxs[i]
				}
			}
		}
		for _, i := range idxs {
			tc := *toolByIndex[i]
			if tc.Type == "" {
				tc.Type = "function"
			}
			msg.ToolCalls = append(msg.ToolCalls, tc)
		}
	}
	if len(out.Choices) == 0 {
		out.Choices = []Choice{{Index: 0, FinishReason: "stop"}}
	}
	out.Choices[0].Message = msg
	out.ElapsedMS = time.Since(start).Milliseconds()
	out.FirstDeltaMS = firstMS
	return out, nil
}

// PingResult 连通性测试结果。
type PingResult struct {
	LatencyMS int64    `json:"latency_ms"`
	Models    []string `json:"models,omitempty"`
	Model     string   `json:"model"`
	Reply     string   `json:"reply,omitempty"`
	Endpoint  string   `json:"endpoint"`
	// Via 说明用哪种方式验证成功：models | chat
	Via string `json:"via"`
}

// Ping 先试 /models（零 token），失败再发一条极短对话。
func (c *Client) Ping(ctx context.Context) (*PingResult, error) {
	start := time.Now()
	models, err := c.ListModels(ctx)
	if err == nil {
		return &PingResult{
			LatencyMS: time.Since(start).Milliseconds(),
			Models:    models,
			Model:     c.model,
			Endpoint:  c.baseURL,
			Via:       "models",
		}, nil
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && (apiErr.Status == 401 || apiErr.Status == 403) {
		return nil, err // Key 错就没必要再花 token 试
	}

	tmp := 0.0
	resp, cerr := c.Chat(ctx, ChatRequest{
		Model:       c.model,
		Messages:    []Message{{Role: "user", Content: "ping"}},
		MaxTokens:   8,
		Temperature: &tmp,
	})
	if cerr != nil {
		return nil, cerr
	}
	return &PingResult{
		LatencyMS: time.Since(start).Milliseconds(),
		Model:     resp.Model,
		Reply:     truncate(resp.Content(), 60),
		Endpoint:  c.baseURL,
		Via:       "chat",
	}, nil
}

// ---------- HTTP ----------

func (c *Client) authorize(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

func (c *Client) post(ctx context.Context, req ChatRequest) ([]byte, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	c.authorize(httpReq)
	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return nil, wrapTransport(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, wrapTransport(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseAPIError(resp.StatusCode, body)
	}
	return body, nil
}

func (c *Client) stream(ctx context.Context, req ChatRequest, withUsage bool) (*http.Response, error) {
	req.Stream = true
	if withUsage {
		req.StreamOptions = &StreamOptions{IncludeUsage: true}
	} else {
		req.StreamOptions = nil
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	c.authorize(httpReq)
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return nil, wrapTransport(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		return nil, parseAPIError(resp.StatusCode, body)
	}
	return resp, nil
}

func parseAPIError(status int, body []byte) error {
	apiErr := &APIError{Status: status, Body: truncate(string(body), 2000)}
	var doc struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &doc); err == nil {
		apiErr.Message = doc.Error.Message
		apiErr.Type = doc.Error.Type
		if apiErr.Message == "" {
			apiErr.Message = doc.Message
		}
		if doc.Error.Code != nil {
			apiErr.Code = fmt.Sprint(doc.Error.Code)
		}
	}
	if apiErr.Message == "" {
		apiErr.Message = truncate(strings.TrimSpace(string(body)), 200)
	}
	return apiErr
}

func wrapTransport(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("llm: 请求超时（可在设置里调大 timeout）：%w", err)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fmt.Errorf("llm: 网络超时：%w", err)
	}
	return fmt.Errorf("llm: 网络请求失败：%w", err)
}

// FriendlyError 把底层错误翻成人能看懂的一句话（直接显示在 AI 面板）。
func FriendlyError(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case 401, 403:
			return "API Key 无效或无权限（检查 Key 是否填错 / 是否过期）"
		case 402:
			return "账户余额不足，请到供应商控制台充值"
		case 404:
			return "端点或模型不存在（检查 base_url 是否含 /v1、模型名是否拼对）"
		case 429:
			return "触发限流或额度用尽，稍后再试"
		case 400:
			if strings.Contains(strings.ToLower(apiErr.Message), "model") {
				return "模型名不被该端点接受：" + apiErr.Message
			}
			return "请求被拒绝：" + apiErr.Message
		case 500, 502, 503, 504:
			return "供应商服务端错误（" + fmt.Sprint(apiErr.Status) + "），稍后重试"
		}
		return fmt.Sprintf("请求失败(%d)：%s", apiErr.Status, apiErr.Message)
	}
	if errors.Is(err, context.Canceled) {
		return "已停止"
	}
	return err.Error()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
