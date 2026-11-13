// Package eval 是 Agent 的评测骨架（PRD §4.9）。
//
// 用法（真实供应商，会消耗 token）：
//
//	SERIALENS_AI_API_KEY=sk-... go run ./cmd/evalagent -cases internal/agent/eval/cases.jsonl
//
// 每条用例：把样本写进临时工程 → 发起任务 A → agent 自跑工具循环 →
// 取最新草稿在全部样本上试跑 → 逐字段对比期望值。
// 失败时把失败详情回喂重试（模拟「3 轮自修复内通过率」）。
//
// 每次 prompt / few-shot 变更必须跑全样本集回归；指标下降不允许合入。
package eval

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"seriallens/internal/agent"
	"seriallens/internal/llm"
	"seriallens/internal/lua"
)

// Case 一条评测用例。
type Case struct {
	Name        string             `json:"name"`
	Category    string             `json:"category"` // 帧头+CRC / IEEE754 / 小端陷阱 / 可变长 …
	Description string             `json:"description"`
	Samples     []string           `json:"samples"`                // HEX 帧
	Expect      map[string]float64 `json:"expect"`                 // 第一条样本的期望字段
	Tolerance   float64            `json:"tolerance"`              // 数值容差
	ExpectChart []string           `json:"expect_chart,omitempty"` // 期望出现的图表通道（可空）
}

// Result 单条评测结果。
type Result struct {
	Name      string         `json:"name"`
	Category  string         `json:"category"`
	Pass      bool           `json:"pass"`
	Attempts  int            `json:"attempts"`
	Tokens    int            `json:"tokens"`
	ElapsedMS int64          `json:"elapsed_ms"`
	Fields    map[string]any `json:"fields,omitempty"`
	Error     string         `json:"error,omitempty"`
}

// Report 整批汇总（PRD §4.9 指标）。
type Report struct {
	Total        int      `json:"total"`
	PassFirstTry int      `json:"pass_first_try"`
	PassAny      int      `json:"pass_any"`
	FirstTryRate float64  `json:"first_try_rate"`
	AnyRate      float64  `json:"any_rate"`
	AvgTokens    float64  `json:"avg_tokens"`
	AvgElapsedMS float64  `json:"avg_elapsed_ms"`
	Results      []Result `json:"results"`
}

// Host 评测专用工程宿主：临时目录，真实编译/沙箱，stub 掉图表与 hints。
type Host struct {
	dir string
}

// NewHost 建临时工程并写入样本。
func NewHost(c Case) (*Host, error) {
	dir, err := os.MkdirTemp("", "sl-eval-*")
	if err != nil {
		return nil, err
	}
	h := &Host{dir: dir}
	if err := os.MkdirAll(filepath.Join(dir, ".seriallens", "samples"), 0o755); err != nil {
		return h, err
	}
	for i, s := range c.Samples {
		name := fmt.Sprintf("case%02d.hex", i+1)
		if err := os.WriteFile(filepath.Join(dir, ".seriallens", "samples", name), []byte(s), 0o644); err != nil {
			return h, err
		}
	}
	return h, nil
}

func (h *Host) ProjectDir() string                   { return h.dir }
func (h *Host) ScriptNames() []string                { return nil }
func (h *Host) ActiveScript() string                 { return "" }
func (h *Host) ReadScript(string) (string, error)    { return "", fmt.Errorf("no script") }
func (h *Host) CompileScript(name, src string) error { return lua.Compile(name, []byte(src)) }
func (h *Host) Charts() (map[string]any, error)      { return nil, nil }
func (h *Host) SaveCharts(map[string]any) error      { return nil }
func (h *Host) SetContextHints(string) error         { return nil }
func (h *Host) ListSamples(limit int) ([]agent.SampleMeta, error) {
	return agent.NewSampleStore(h.dir).List(limit)
}
func (h *Host) ReadSample(id string) ([]byte, error) { return agent.NewSampleStore(h.dir).Read(id) }
func (h *Host) ListRecordings() ([]agent.RecordingMeta, error) {
	return agent.NewSampleStore(h.dir).Recordings()
}
func (h *Host) BuiltinAPI() []lua.APIDoc { return lua.APIManifest() }

func (h *Host) SaveDraft(name, src string) (string, error) {
	d := filepath.Join(h.dir, ".seriallens", "drafts")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	rel := filepath.Join(".seriallens", "drafts", name)
	return rel, os.WriteFile(filepath.Join(h.dir, rel), []byte(src), 0o644)
}

func (h *Host) RunSandbox(src string, samples [][]byte, timeout time.Duration) []agent.RunOutcome {
	out := make([]agent.RunOutcome, 0, len(samples))
	rt := lua.New(lua.Options{Timeout: timeout})
	defer rt.Close()
	if err := rt.LoadScript("draft.lua", []byte(src)); err != nil {
		d := lua.DiagnosticFromError("draft.lua", err)
		for range samples {
			out = append(out, agent.RunOutcome{OK: false, Error: d.Message, Diagnostic: &d})
		}
		return out
	}
	for _, raw := range samples {
		fields, points, err := rt.CallOnFrame(raw)
		if err != nil {
			d := lua.DiagnosticFromError("draft.lua", err)
			out = append(out, agent.RunOutcome{OK: false, Error: d.Message, Diagnostic: &d})
			continue
		}
		pts := make([]map[string]any, 0, len(points))
		for _, p := range points {
			pts = append(pts, map[string]any{"ch": p.Channel, "v": p.Value})
		}
		out = append(out, agent.RunOutcome{OK: true, Fields: fields, Points: pts})
	}
	return out
}

// Cleanup 删除临时工程。
func (h *Host) Cleanup() { _ = os.RemoveAll(h.dir) }

// LoadCases 读取用例集。
func LoadCases(path string) ([]Case, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []Case
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		var c Case
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("bad case line: %w", err)
		}
		cases = append(cases, c)
	}
	return cases, nil
}

// Options 评测参数。
type Options struct {
	Settings    llm.Settings
	MaxAttempts int // 自修复轮次（PRD 默认 3）
	Timeout     time.Duration
}

// RunCase 跑一条用例，返回结果。
func RunCase(ctx context.Context, c Case, opt Options) (res Result) {
	res = Result{Name: c.Name, Category: c.Category}
	start := time.Now()
	defer func() { res.ElapsedMS = time.Since(start).Milliseconds() }()

	h, err := NewHost(c)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer h.Cleanup()

	exec := agent.NewExecutor(h, nil) // 评测不需要 UI 确认
	var conv []llm.Message
	userMsg := c.Description + "\n\n样本已在工程的 .seriallens/samples/ 下（case1.hex 起）。"

	for attempt := 1; attempt <= max(1, opt.MaxAttempts); attempt++ {
		res.Attempts = attempt
		runner := &agent.Runner{
			Client:    llm.NewClient(opt.Settings),
			Model:     opt.Settings.Model,
			MaxTokens: opt.Settings.MaxTokens,
			Exec:      exec,
			RunID:     fmt.Sprintf("eval-%s-%d", c.Name, attempt),
		}
		conv = append(conv, llm.Message{Role: "user", Content: userMsg})
		msgs := runner.BuildMessages(agent.TaskScript, agent.Context{Samples: mustSamples(h)}, conv)

		runCtx, cancel := context.WithTimeout(ctx, opt.Timeout)
		out, err := runner.Run(runCtx, agent.TaskScript, msgs, nil)
		cancel()
		if out != nil && out.Usage != nil {
			res.Tokens += out.Usage.TotalTokens
		}
		if err != nil {
			if ctx.Err() != nil {
				res.Error = "canceled"
				return res
			}
			res.Error = llm.FriendlyError(err)
			return res
		}
		// 空 assistant 消息会被供应商拒（Invalid assistant message），跳过
		if strings.TrimSpace(out.Content) != "" {
			conv = append(conv, llm.Message{Role: "assistant", Content: out.Content})
		}

		// 拿最新草稿在全部样本上试跑（不信任模型口头的「试跑通过」）
		fields, err := tryLatestDraft(h, c)
		res.Fields = fields
		if err == nil {
			res.Pass = fieldsMatch(fields, c.Expect, c.Tolerance)
		}
		if res.Pass {
			return res
		}
		// 失败详情回喂，进入下一轮自修复
		userMsg = fmt.Sprintf("上一版脚本没有通过验收：%s\n期望字段：%v（容差 %v）。请修复后重新走 compile → 试跑 → 存草稿。",
			failureDetail(h, c), c.Expect, c.Tolerance)
	}
	res.Error = fmt.Sprintf("%d 轮自修复后仍未通过", opt.MaxAttempts)
	return res
}

// tryLatestDraft 用最新草稿逐样本试跑，返回第一条样本的字段。
func tryLatestDraft(h *Host, c Case) (map[string]any, error) {
	draftDir := filepath.Join(h.dir, ".seriallens", "drafts")
	entries, err := os.ReadDir(draftDir)
	if err != nil || len(entries) == 0 {
		return nil, fmt.Errorf("agent 没有产出草稿")
	}
	latest := ""
	var latestMod time.Time
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if latest == "" || info.ModTime().After(latestMod) {
			latest, latestMod = e.Name(), info.ModTime()
		}
	}
	src, err := os.ReadFile(filepath.Join(draftDir, latest))
	if err != nil {
		return nil, err
	}

	rt := lua.New(lua.Options{Timeout: 5 * time.Second})
	defer rt.Close()
	if err := rt.LoadScript(latest, src); err != nil {
		return nil, fmt.Errorf("draft compile: %v", lua.DiagnosticFromError(latest, err).Message)
	}

	fields, _, err := rt.CallOnFrame(decodeSample(c.Samples[0]))
	if err != nil {
		return nil, fmt.Errorf("draft run: %v", err)
	}
	return fields, nil
}

func fieldsMatch(fields map[string]any, expect map[string]float64, tol float64) bool {
	if len(expect) == 0 {
		return fields != nil && len(fields) > 0
	}
	for k, want := range expect {
		got, ok := numField(fields, k)
		if !ok {
			return false
		}
		d := got - want
		if d < 0 {
			d = -d
		}
		if tol <= 0 {
			tol = 1e-6
		}
		if d > tol {
			return false
		}
	}
	return true
}

func numField(fields map[string]any, key string) (float64, bool) {
	switch v := fields[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}

func failureDetail(h *Host, c Case) string {
	fields, err := tryLatestDraft(h, c)
	if err != nil {
		return err.Error()
	}
	raw, _ := json.Marshal(fields)
	return fmt.Sprintf("实际字段：%s", raw)
}

func mustSamples(h *Host) []agent.SampleMeta {
	list, _ := h.ListSamples(10)
	return list
}

func decodeSample(hexStr string) []byte {
	raw, err := hex.DecodeString(strings.Map(func(r rune) rune {
		switch r {
		case ' ', ',', '\t', '\r', '\n', '-':
			return -1
		}
		return r
	}, strings.TrimSpace(hexStr)))
	if err != nil {
		return nil
	}
	return raw
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Summarize 汇总 PRD §4.9 指标。
func Summarize(results []Result) Report {
	rep := Report{Total: len(results), Results: results}
	var tokens, elapsed int64
	for _, r := range results {
		if r.Pass {
			rep.PassAny++
			if r.Attempts == 1 {
				rep.PassFirstTry++
			}
		}
		tokens += int64(r.Tokens)
		elapsed += r.ElapsedMS
	}
	if rep.Total > 0 {
		rep.FirstTryRate = float64(rep.PassFirstTry) / float64(rep.Total)
		rep.AnyRate = float64(rep.PassAny) / float64(rep.Total)
		rep.AvgTokens = float64(tokens) / float64(rep.Total)
		rep.AvgElapsedMS = float64(elapsed) / float64(rep.Total)
	}
	return rep
}
