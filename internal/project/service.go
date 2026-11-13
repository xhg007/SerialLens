package project

import (
	"sync"

	"seriallens/internal/events"
)

// State 工程生命周期状态（PRD §3.8）。
type State int

const (
	StateNoProject State = iota
	StateProjectOpen
)

// Service 工程域核心枢纽：路径单一来源（PRD §7.5），
// 所有需要工程路径的 Service 必须经 Current() 获取。
type Service struct {
	mu      sync.Mutex
	state   State
	current string
	meta    *ProjectMeta
	recents *RecentStore
	emit    events.Emitter
}

// NewService 创建服务。emit 可为 nil（纯测试）。
func NewService(recents *RecentStore, emit events.Emitter) *Service {
	if emit == nil {
		emit = func(string, map[string]any) {}
	}
	return &Service{recents: recents, emit: emit}
}

// State 返回当前状态。
func (s *Service) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Current 返回当前工程目录与元数据；无工程时 ok=false。
func (s *Service) Current() (dir string, meta *ProjectMeta, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != StateProjectOpen {
		return "", nil, false
	}
	return s.current, s.meta, true
}

// Create 生成目录骨架并打开工程。
func (s *Service) Create(dir, name, description string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := Scaffold(dir, name, description)
	if err != nil {
		return err
	}
	s.meta = meta
	s.current = dir
	s.state = StateProjectOpen
	_ = s.recents.RecordOpen(dir, name)
	s.emitOpen()
	return nil
}

// Open 打开或切换工程（事务顺序：新工程加载成功才替换现有状态；
// 失败时保持现有状态不变并 emit EvProjectError —— M2+ 将在此插入
// 串口断开与 Lua 字节码清理）。
func (s *Service) Open(dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := LoadMeta(dir)
	if err != nil {
		s.emit(events.EvProjectError, map[string]any{
			"code":    "project_load_failed",
			"message": err.Error(),
		})
		return err
	}
	s.meta = meta
	s.current = dir
	s.state = StateProjectOpen
	_ = s.recents.RecordOpen(dir, meta.Name)
	s.emitOpen()
	return nil
}

// Close 关闭当前工程。
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != StateProjectOpen {
		return nil
	}
	s.meta = nil
	s.current = ""
	s.state = StateNoProject
	s.emit(events.EvProjectClosed, map[string]any{})
	return nil
}

func (s *Service) emitOpen() {
	sc := s.meta.SerialConfig
	s.emit(events.EvProjectOpened, map[string]any{
		"path":          s.current,
		"name":          s.meta.Name,
		"serial_config": map[string]any{"baudrate": sc.Baudrate, "data_bits": sc.DataBits, "stop_bits": sc.StopBits, "parity": sc.Parity, "flow_control": sc.FlowControl, "last_port": sc.LastPort},
		"active_script": s.meta.ActiveScript,
	})
}
