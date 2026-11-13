// Package agent 是 AI 助手的「工具层 + 会话层 + 工具调用循环」（PRD §4.3~§4.6）。
//
// 刻意与 app 包解耦：所有副作用都经 Host 接口，测试里可以用假 Host 跑完整循环，
// 也让 eval（internal/agent/eval）能在没有 Wails 的情况下跑回归。
//
// 安全边界（PRD §4.8）：
//   - 工具白名单只有下面 12 个，没有 write_file / send_serial / delete；
//   - 唯一可写路径是工程内 .seriallens/drafts/；
//   - 沙箱试跑由 Host 提供，单样本 5s 超时。
package agent

import (
	"context"
	"time"

	"seriallens/internal/lua"
)

// Task 双任务模式（PRD §4.3）：两套工具白名单互不交叉。
type Task string

const (
	TaskScript Task = "script" // A：生成/修复 Lua 协议脚本
	TaskCharts Task = "charts" // B：根据脚本同步 charts.json
)

// SampleMeta 一条抓包样本的元信息。
type SampleMeta struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Size    int    `json:"size"`
	ModTime int64  `json:"mod_time"`
	Preview string `json:"preview"` // 前 24 字节 HEX，便于模型挑选
}

// RecordingMeta data/ 下的录制文件元信息（只读头部，不解码）。
type RecordingMeta struct {
	File      string `json:"file"`
	Size      int64  `json:"size"`
	Port      string `json:"port,omitempty"`
	Baudrate  int    `json:"baudrate,omitempty"`
	Samples   int    `json:"samples,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	Note      string `json:"note,omitempty"` // 头部读不出来时给模型的解释
}

// RunOutcome 单条样本的沙箱试跑结果。
type RunOutcome struct {
	SampleID   string           `json:"sample_id"`
	OK         bool             `json:"ok"`
	Fields     map[string]any   `json:"fields,omitempty"`
	Points     []map[string]any `json:"points,omitempty"`
	Error      string           `json:"error,omitempty"`
	Diagnostic *lua.Diagnostic  `json:"diagnostic,omitempty"`
	DurationUS int64            `json:"duration_us"`
}

// Host 工具层需要的全部工程能力；由 app 包实现，测试/eval 可用内存实现替换。
type Host interface {
	// ProjectDir 当前工程根目录；未打开工程返回 ""。
	ProjectDir() string
	// ScriptNames 当前工程 scripts/ 下的脚本名（含活动脚本）。
	ScriptNames() []string
	// ActiveScript 活动脚本名。
	ActiveScript() string
	// ReadScript 读取脚本源码（相对 scripts/ 的文件名）。
	ReadScript(name string) (string, error)
	// CompileScript 只做编译检查，不落盘。
	CompileScript(name, src string) error
	// RunSandbox 在一次性沙箱里跑 on_frame，逐样本返回结果；timeout 为单样本上限。
	RunSandbox(src string, samples [][]byte, timeout time.Duration) []RunOutcome
	// Charts 当前 charts.json（未加载时返回 nil）。
	Charts() (map[string]any, error)
	// SaveCharts 校验后写入 charts.json 并广播 charts:updated。
	SaveCharts(cfg map[string]any) error
	// SaveDraft 写 .seriallens/drafts/<name>，返回相对路径。
	SaveDraft(name, src string) (string, error)
	// SetContextHints 写 project.json 的 agent_settings.context_hints。
	SetContextHints(hints string) error
	// ListSamples 列出 .seriallens/samples/ 下的样本（按修改时间倒序）。
	ListSamples(limit int) ([]SampleMeta, error)
	// ReadSample 读取样本原始字节。
	ReadSample(id string) ([]byte, error)
	// ListRecordings 列出 data/ 下的 .srec 录制。
	ListRecordings() ([]RecordingMeta, error)
	// BuiltinAPI 内置 Lua API 清单（与编辑器补全同源）。
	BuiltinAPI() []lua.APIDoc
}

// ConfirmRequest 一次「持久化动作确认」（UI 阻塞等待）。
type ConfirmRequest struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Diff        string `json:"diff,omitempty"` // 统一 diff，前端渲染
	Kind        string `json:"kind"`           // script_save | context_hints | generic
	Target      string `json:"target,omitempty"`
	Content     string `json:"content,omitempty"` // 待写入内容（应用时前端直接用）
}

// Confirmer 由 UI 实现：发出确认请求并等待用户决定。
type Confirmer interface {
	Confirm(ctx context.Context, req ConfirmRequest) (approved bool, err error)
}

// ConfirmerFunc 便于测试。
type ConfirmerFunc func(ctx context.Context, req ConfirmRequest) (bool, error)

// Confirm 实现 Confirmer。
func (f ConfirmerFunc) Confirm(ctx context.Context, req ConfirmRequest) (bool, error) {
	return f(ctx, req)
}
