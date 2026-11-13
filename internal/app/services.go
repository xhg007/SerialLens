package app

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"seriallens/internal/charts"
	"seriallens/internal/events"
	"seriallens/internal/lua"
	"seriallens/internal/parser"
	"seriallens/internal/project"
)

// Services 装配层：把串口、Lua 运行时、解析管线、工程域按 PRD §7.2 的数据流连起来。
//
//	SerialService ─tap→ parser.Ingest → framer → LuaRuntime → frame:parsed / chart:point
//
// 单独成层是为了让 Wails 服务保持薄壳，同时避免 project → app 的循环依赖。
type Services struct {
	Project *ProjectService
	Serial  *SerialService
	Parser  *ParserAPI
	Charts  *ChartsAPI

	runtime *lua.Runtime
	pipe    *parser.Service
	charts  *charts.Service

	mu   sync.RWMutex
	emit events.Emitter
	once sync.Once
}

// NewServices 构建全部服务并接线（尚未注入 Wails 事件桥接）。
func NewServices() *Services {
	s := &Services{}
	s.Serial = NewSerialServiceWithDefault()
	s.runtime = lua.New(lua.Options{
		Sinks: lua.Sinks{
			Send: s.Serial.SendRaw,
			Log: func(level, msg string) {
				s.emitEvent(events.EvScriptLog, map[string]any{
					"level": level, "msg": msg, "ts": nowMs(),
				})
			},
		},
	})
	s.pipe = parser.New(parser.Options{Runtime: s.runtime, Emit: s.emitEvent})
	s.Serial.SetRawTap(s.pipe.Ingest)
	s.charts = charts.NewService(s.emitEvent)
	s.Project = newProjectService(s)
	s.Parser = &ParserAPI{s: s}
	s.Charts = &ChartsAPI{s: s}
	s.Project.svc.SetHooks(&switchHooks{s: s})
	return s
}

// install 注入 Wails 事件桥接并启动解析消费循环（幂等）。
func (s *Services) install(app *application.App) {
	if app == nil {
		return
	}
	s.once.Do(func() {
		em := func(event string, payload map[string]any) {
			app.Event.Emit(event, payload)
		}
		s.mu.Lock()
		s.emit = em
		s.mu.Unlock()
		s.Project.svc.SetEmitter(em)
		s.Serial.SetEmitter(em)
		s.charts.SetEmitter(em)
		s.pipe.Start()
	})
}

func (s *Services) emitEvent(event string, payload map[string]any) {
	s.mu.RLock()
	em := s.emit
	s.mu.RUnlock()
	if em != nil {
		em(event, payload)
	}
}

// Shutdown 释放运行时资源（应用退出）。
func (s *Services) Shutdown() {
	s.charts.Close()
	s.pipe.Stop()
	s.runtime.Close()
}

// switchHooks 实现 project.SwitchHooks：工程切换时的外部资源拆卸/装载。
type switchHooks struct{ s *Services }

// BeforeSwitch 顺序按 PRD §3.8：先断串口，再清 Lua 字节码，最后停图表监听。
func (h *switchHooks) BeforeSwitch() error {
	if err := h.s.Serial.Disconnect(); err != nil {
		return fmt.Errorf("disconnect serial: %w", err)
	}
	h.s.pipe.Clear()
	h.s.charts.Close()
	return nil
}

// AfterOpen 加载新工程的 active_script 与 charts.json；任一失败都不关闭工程。
func (h *switchHooks) AfterOpen(dir string, meta *project.ProjectMeta) {
	if meta != nil && meta.ActiveScript != "" {
		if err := h.s.pipe.LoadScript(dir, meta.ActiveScript); err != nil {
			h.s.emitEvent(events.EvScriptError, map[string]any{
				"script": meta.ActiveScript, "error": err.Error(), "line": 0,
			})
		}
	}
	// 图表配置独立加载：脚本坏了图表仍可展示；charts:error 由 charts.Service 自行上报。
	_ = h.s.charts.Load(dir)
}

// ParserAPI Wails 薄壳：解析统计、手动重载、单帧试跑、命令编码下发。
type ParserAPI struct {
	s *Services
}

// ServiceStartup 与 ProjectService 一样在启动时注入事件桥接（install 幂等）。
func (p *ParserAPI) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	p.s.install(application.Get())
	return nil
}

func nowMs() int64 { return time.Now().UnixMilli() }

// GetParserStats 返回解析管线统计（含 bytecode 残留数，用于验收）。
func (p *ParserAPI) GetParserStats() map[string]any {
	return p.s.pipe.Stats()
}

// ReloadActiveScript 重新加载当前工程的 active_script（热重载 M4 会自动化）。
func (p *ParserAPI) ReloadActiveScript() error {
	dir, meta, ok := p.s.Project.svc.Current()
	if !ok {
		return fmt.Errorf("no project open")
	}
	if meta.ActiveScript == "" {
		return fmt.Errorf("project has no active script")
	}
	return p.s.pipe.LoadScript(dir, meta.ActiveScript)
}

// DecodeHexFrame 用当前脚本试跑一帧（终端右键「解析此帧」/ M4 编辑器测试用）。
func (p *ParserAPI) DecodeHexFrame(hexStr string) (map[string]any, error) {
	raw, err := decodeHex(hexStr)
	if err != nil {
		return nil, err
	}
	fields, points, err := p.s.runtime.CallOnFrame(raw)
	if err != nil {
		return nil, err
	}
	pts := make([]map[string]any, 0, len(points))
	for _, pt := range points {
		pts = append(pts, map[string]any{"ch": pt.Channel, "v": pt.Value})
	}
	return map[string]any{"fields": fields, "points": pts}, nil
}

// SendCommand 调用脚本 encode_command 编码并通过串口下发（M6 命令面板的地基）。
func (p *ParserAPI) SendCommand(cmd string, params map[string]any) (string, error) {
	data, err := p.s.runtime.CallEncodeCommand(cmd, params)
	if err != nil {
		return "", err
	}
	if err := p.s.Serial.SendRaw(data); err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(data)), nil
}

func decodeHex(s string) ([]byte, error) {
	clean := strings.Join(strings.Fields(s), "")
	if clean == "" {
		return nil, fmt.Errorf("empty hex payload")
	}
	raw, err := hex.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("invalid hex: %w", err)
	}
	return raw, nil
}
