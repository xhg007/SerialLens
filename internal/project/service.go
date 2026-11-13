package project

import (
	"fmt"
	"sync"

	"seriallens/internal/events"
)

// State 工程生命周期状态（PRD §3.8）。
type State int

const (
	StateNoProject State = iota
	StateProjectOpen
	// StateProjectSwitching 切换事务进行中（PRD §3.8）：旧工程资源已开始拆卸，
	// 新工程尚未激活。此状态不对外暴露为终态，仅用于拒绝并发切换。
	StateProjectSwitching
)

// SwitchHooks 由壳层注入的切换钩子：拆卸/装载工程外资源（串口、Lua Runtime）。
// 放在 project 包内定义为接口，避免 project → app 的循环依赖。
type SwitchHooks interface {
	// BeforeSwitch 在新工程激活前拆卸旧资源（断开串口、清空字节码缓存）。
	// 返回错误则中止切换并保持原工程。
	BeforeSwitch() error
	// AfterOpen 新工程激活后装载资源（加载 active_script）。
	// 失败不回滚工程（脚本坏了不应把工程关闭），错误自行上报。
	AfterOpen(dir string, meta *ProjectMeta)
}

// Service 工程域核心枢纽：路径单一来源（PRD §7.5），
// 所有需要工程路径的 Service 必须经 Current() 获取。
type Service struct {
	mu      sync.Mutex
	state   State
	current string
	meta    *ProjectMeta
	recents *RecentStore
	emit    events.Emitter
	hooks   SwitchHooks
}

// NewService 创建服务。emit 可为 nil（纯测试）。
func NewService(recents *RecentStore, emit events.Emitter) *Service {
	if emit == nil {
		emit = func(string, map[string]any) {}
	}
	return &Service{recents: recents, emit: emit}
}

// SetEmitter 在 Wails ServiceStartup 阶段注入事件桥接。
func (s *Service) SetEmitter(fn events.Emitter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fn != nil {
		s.emit = fn
	}
}

// SetHooks 注入切换钩子（壳层在装配阶段调用一次）。
func (s *Service) SetHooks(h SwitchHooks) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks = h
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
// 磁盘 IO 与 emit 均在锁外，锁内只做状态替换（避免监听者回调自死锁）。
func (s *Service) Create(dir, name, description string) error {
	meta, err := Scaffold(dir, name, description)
	if err != nil {
		return err
	}
	return s.activate(dir, meta)
}

// Open 打开或切换工程（事务顺序：新工程加载成功才替换现有状态；
// 失败时保持现有状态不变并 emit EvProjectError —— M2+ 将在此插入
// 串口断开与 Lua 字节码清理）。
func (s *Service) Open(dir string) error {
	// 1) 预校验：新工程读不出来就什么都不拆，直接保持原状态（禁止半加载）。
	meta, err := LoadMeta(dir)
	if err != nil {
		s.emit(events.EvProjectError, map[string]any{
			"code":    "project_load_failed",
			"message": err.Error(),
		})
		return err
	}
	return s.activate(dir, meta)
}

// activate 激活工程：需要切换时走 ProjectSwitching 拆卸流程。
func (s *Service) activate(dir string, meta *ProjectMeta) error {
	s.mu.Lock()
	sameDir := s.state == StateProjectOpen && s.current == dir
	switching := s.state == StateProjectOpen && !sameDir
	prevDir, prevMeta := s.current, s.meta
	if switching {
		s.state = StateProjectSwitching
	} else if s.state == StateProjectSwitching {
		s.mu.Unlock()
		return fmt.Errorf("project switch already in progress")
	}
	hooks := s.hooks
	s.mu.Unlock()

	if switching && hooks != nil {
		if err := hooks.BeforeSwitch(); err != nil {
			s.mu.Lock()
			s.current, s.meta = prevDir, prevMeta
			s.state = StateProjectOpen
			s.mu.Unlock()
			s.emit(events.EvProjectError, map[string]any{
				"code":    "project_switch_failed",
				"message": err.Error(),
			})
			return err
		}
	}

	payload := openedPayload(dir, meta)
	s.mu.Lock()
	s.meta = meta
	s.current = dir
	s.state = StateProjectOpen
	s.mu.Unlock()

	_ = s.recents.RecordOpen(dir, meta.Name)
	s.emit(events.EvProjectOpened, payload)
	if hooks != nil {
		hooks.AfterOpen(dir, meta)
	}
	return nil
}

// Close 关闭当前工程：先拆卸外部资源（串口/字节码），再落到 NoProject。
// 拆卸失败不阻塞关闭（工程必须能被关掉），错误通过 project:error 上报。
func (s *Service) Close() error {
	s.mu.Lock()
	if s.state != StateProjectOpen {
		s.mu.Unlock()
		return nil
	}
	s.state = StateProjectSwitching
	hooks := s.hooks
	s.mu.Unlock()

	if hooks != nil {
		if err := hooks.BeforeSwitch(); err != nil {
			s.emit(events.EvProjectError, map[string]any{
				"code":    "project_close_cleanup_failed",
				"message": err.Error(),
			})
		}
	}

	s.mu.Lock()
	s.meta = nil
	s.current = ""
	s.state = StateNoProject
	s.mu.Unlock()
	s.emit(events.EvProjectClosed, map[string]any{})
	return nil
}

func openedPayload(dir string, meta *ProjectMeta) map[string]any {
	sc := meta.SerialConfig
	return map[string]any{
		"path":          dir,
		"name":          meta.Name,
		"serial_config": map[string]any{"baudrate": sc.Baudrate, "data_bits": sc.DataBits, "stop_bits": sc.StopBits, "parity": sc.Parity, "flow_control": sc.FlowControl, "last_port": sc.LastPort},
		"active_script": meta.ActiveScript,
	}
}
