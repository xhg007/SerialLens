package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"seriallens/internal/agent"
	"seriallens/internal/charts"
	"seriallens/internal/lua"
)

// agentHost 把工程/脚本/图表/样本能力适配给 internal/agent 的工具层。
//
// 这一层只做「读当前状态、写允许写的位置」，不做任何策略判断：
// 工具白名单、任务隔离、确认关卡都在 agent 包里。
type agentHost struct {
	s *Services
}

func (h *agentHost) ProjectDir() string {
	if h == nil || h.s == nil || h.s.Project == nil {
		return ""
	}
	dir, _, ok := h.s.Project.svc.Current()
	if !ok {
		return ""
	}
	return dir
}

func (h *agentHost) ScriptNames() []string {
	if h == nil || h.s == nil || h.s.scripts == nil {
		return nil
	}
	files, err := h.s.scripts.List()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Name)
	}
	return out
}

func (h *agentHost) ActiveScript() string {
	if h == nil || h.s == nil || h.s.scripts == nil {
		return ""
	}
	return h.s.scripts.Active()
}

func (h *agentHost) ReadScript(name string) (string, error) {
	if h == nil || h.s == nil || h.s.scripts == nil {
		return "", fmt.Errorf("no scripts service")
	}
	return h.s.scripts.Read(name)
}

func (h *agentHost) CompileScript(name, src string) error {
	return lua.Compile(name, []byte(src))
}

// RunSandbox 一次性 Runtime 逐样本试跑：send_bytes 被禁用，触不到串口；
// Timeout 由 Runtime 内部 SetContext 强制（死循环脚本会被 ErrTimeout 打断）。
func (h *agentHost) RunSandbox(src string, samples [][]byte, timeout time.Duration) []agent.RunOutcome {
	out := make([]agent.RunOutcome, 0, len(samples))
	rt := lua.New(lua.Options{
		Timeout: timeout,
		Sinks: lua.Sinks{
			Send: func([]byte) error { return fmt.Errorf("send_bytes is disabled in sandbox") },
		},
	})
	defer rt.Close()

	if err := rt.LoadScript("draft.lua", []byte(src)); err != nil {
		d := lua.DiagnosticFromError("draft.lua", err)
		for range samples {
			out = append(out, agent.RunOutcome{OK: false, Error: d.Message, Diagnostic: &d})
		}
		return out
	}

	for _, raw := range samples {
		start := time.Now()
		fields, points, err := rt.CallOnFrame(raw)
		dur := time.Since(start).Microseconds()
		if err != nil {
			d := lua.DiagnosticFromError("draft.lua", err)
			out = append(out, agent.RunOutcome{OK: false, Error: d.Message, Diagnostic: &d, DurationUS: dur})
			continue
		}
		pts := make([]map[string]any, 0, len(points))
		for _, p := range points {
			pts = append(pts, map[string]any{"ch": p.Channel, "v": p.Value})
		}
		out = append(out, agent.RunOutcome{OK: true, Fields: fields, Points: pts, DurationUS: dur})
	}
	return out
}

func (h *agentHost) Charts() (map[string]any, error) {
	if h == nil || h.s == nil || h.s.charts == nil {
		return nil, nil
	}
	cfg := h.s.charts.Config()
	if cfg == nil {
		return nil, nil
	}
	return chartsToMap(cfg)
}

func (h *agentHost) SaveCharts(raw map[string]any) error {
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var cfg charts.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("invalid charts config: %w", err)
	}
	if h == nil || h.s == nil || h.s.charts == nil {
		return fmt.Errorf("no charts service")
	}
	return h.s.charts.SaveConfig(&cfg)
}

// SaveDraft 只写 .seriallens/drafts/ —— AI 唯一可写的目录（PRD §4.8）。
func (h *agentHost) SaveDraft(name, src string) (string, error) {
	dir := h.ProjectDir()
	if dir == "" {
		return "", fmt.Errorf("no project open")
	}
	if err := plainFileName(name); err != nil {
		return "", err
	}
	rel := filepath.Join(".seriallens", "drafts", name)
	abs := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(src), 0o644); err != nil {
		return "", err
	}
	return rel, nil
}

func (h *agentHost) SetContextHints(hints string) error {
	return h.s.Project.svc.SetContextHints(hints)
}

func (h *agentHost) ListSamples(limit int) ([]agent.SampleMeta, error) {
	return agent.NewSampleStore(h.ProjectDir()).List(limit)
}

func (h *agentHost) ReadSample(id string) ([]byte, error) {
	return agent.NewSampleStore(h.ProjectDir()).Read(id)
}

func (h *agentHost) ListRecordings() ([]agent.RecordingMeta, error) {
	return agent.NewSampleStore(h.ProjectDir()).Recordings()
}

func (h *agentHost) BuiltinAPI() []lua.APIDoc { return lua.APIManifest() }

// plainFileName 拒绝路径穿越与隐藏目录名。
func plainFileName(name string) error {
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return fmt.Errorf("invalid file name %q", name)
	}
	return nil
}

func chartsToMap(cfg *charts.Config) (map[string]any, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
