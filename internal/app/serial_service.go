package app

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"seriallens/internal/events"
	"seriallens/internal/serial"
)

// batchIntervalMs 串口数据合帧间隔（PRD ADR-02：33ms 批量推送）。
const batchIntervalMs = 33

// SerialService Wails 薄壳：串口连接、读 goroutine、批量推送。
type SerialService struct {
	port serial.Port
	emit events.Emitter

	mu        sync.Mutex
	connected bool
	bytesIn   uint64
	bytesOut  uint64
	stop      chan struct{}
	done      chan struct{}
	reading   bool

	// rawTap 原始字节旁路（M2 解析管线）：在读循环内同步调用，实现方必须非阻塞。
	tap func([]byte)
}

func NewSerialService(port serial.Port, emit events.Emitter) *SerialService {
	if emit == nil {
		emit = func(string, map[string]any) {}
	}
	return &SerialService{port: port, emit: emit, stop: make(chan struct{})}
}

// NewSerialServiceWithDefault 使用 go.bug.st 系统串口实现（Wails 注册用）。
func NewSerialServiceWithDefault() *SerialService {
	return NewSerialService(serial.NewPort(), nil)
}

// ServiceStartup 在应用启动时注入事件桥接。
func (s *SerialService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	if app := application.Get(); app != nil {
		s.SetEmitter(func(event string, payload map[string]any) {
			app.Event.Emit(event, payload)
		})
	}
	s.StartHotplugWatch(ctx)
	return nil
}

// SetEmitter 在 Wails ServiceStartup 阶段注入事件桥接。
func (s *SerialService) SetEmitter(fn events.Emitter) {
	if fn == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emit = fn
}

// SetRawTap 注入原始字节旁路（解析管线）。实现必须非阻塞，否则会拖慢串口读。
func (s *SerialService) SetRawTap(fn func([]byte)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tap = fn
}

// SendRaw 供 Lua send_bytes 使用：跳过 hex 解析，直接写串口。
func (s *SerialService) SendRaw(data []byte) error {
	s.mu.Lock()
	connected := s.connected
	s.mu.Unlock()
	if !connected {
		return fmt.Errorf("not connected")
	}
	n, err := s.port.Write(data)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.bytesOut += uint64(n)
	s.mu.Unlock()
	return nil
}

func (s *SerialService) rawTap() func([]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tap
}

// ListPorts 枚举系统串口。
func (s *SerialService) ListPorts() []map[string]any {
	ports, err := serial.Enumerate()
	if err != nil {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(ports))
	for _, p := range ports {
		out = append(out, map[string]any{
			"name": p.Name, "friendly": p.FriendlyName, "vid": p.VID, "pid": p.PID,
		})
	}
	return out
}

// Connect 按参数打开串口并启动读 goroutine。
// key: port, baudrate, data_bits, stop_bits, parity, flow_control。
func (s *SerialService) Connect(cfg map[string]any) error {
	c, err := parseConfig(cfg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.connected {
		s.mu.Unlock()
		return fmt.Errorf("already connected")
	}
	s.mu.Unlock()
	if err := s.port.Open(c); err != nil {
		return err
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	s.mu.Lock()
	s.connected = true
	s.reading = true
	s.stop = stop
	s.done = done
	s.mu.Unlock()
	go s.readLoop(stop, done)
	s.emitStatus()
	return nil
}

// emitter 并发安全读取事件桥接（readLoop 在无锁路径使用）。
func (s *SerialService) emitter() events.Emitter {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emit
}

func parseConfig(cfg map[string]any) (serial.Config, error) {
	getInt := func(key string, def int) (int, error) {
		v, ok := cfg[key]
		if !ok || v == nil {
			return def, nil
		}
		switch n := v.(type) {
		case int:
			return n, nil
		case float64:
			return int(n), nil
		case int64:
			return int(n), nil
		default:
			return 0, fmt.Errorf("field %q must be a number", key)
		}
	}
	getStr := func(key, def string) (string, error) {
		v, ok := cfg[key]
		if !ok || v == nil {
			return def, nil
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("field %q must be a string", key)
		}
		return s, nil
	}
	var c serial.Config
	var err error
	if c.PortName, err = getStr("port", ""); err != nil {
		return c, err
	}
	if c.BaudRate, err = getInt("baudrate", 115200); err != nil {
		return c, err
	}
	if c.DataBits, err = getInt("data_bits", 8); err != nil {
		return c, err
	}
	if c.StopBits, err = getInt("stop_bits", 1); err != nil {
		return c, err
	}
	if c.Parity, err = getStr("parity", "N"); err != nil {
		return c, err
	}
	if c.FlowControl, err = getStr("flow_control", "none"); err != nil {
		return c, err
	}
	return c, nil
}

// Disconnect 关闭串口并等待读 goroutine 退出（每次连接独立 stop/done，
// 不复用 Once —— 否则重连后 close 永不生效，泄漏 goroutine）。
func (s *SerialService) Disconnect() error {
	s.mu.Lock()
	if !s.connected {
		s.mu.Unlock()
		return nil
	}
	stop, done := s.stop, s.done
	s.stop, s.done = nil, nil
	s.connected = false
	s.mu.Unlock()

	if stop != nil {
		close(stop)
	}
	err := s.port.Close()
	if done != nil {
		select {
		case <-done:
		case <-time.After(time.Second):
			// 驱动未响应 Close：readLoop 仍会因读错误退出，不阻塞 UI
		}
	}
	s.emitStatus()
	return err
}

// Send 发送数据。hexMode=true 时 data 为空格分隔的 HEX 字符串；否则原样发送。
func (s *SerialService) Send(data string, hexMode bool) error {
	s.mu.Lock()
	connected := s.connected
	s.mu.Unlock()
	if !connected {
		return fmt.Errorf("not connected")
	}
	var payload []byte
	if hexMode {
		clean := strings.Join(strings.Fields(data), "")
		if clean == "" {
			return fmt.Errorf("empty hex payload")
		}
		b, err := hex.DecodeString(clean)
		if err != nil {
			return fmt.Errorf("invalid hex: %w", err)
		}
		payload = b
	} else {
		payload = []byte(data)
	}
	n, err := s.port.Write(payload)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.bytesOut += uint64(n)
	s.mu.Unlock()
	s.emitStatus()
	return nil
}

// GetStatus 返回连接状态与字节计数。
func (s *SerialService) GetStatus() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := "disconnected"
	if s.connected {
		state = "connected"
	}
	return map[string]any{
		"state":     state,
		"bytes_in":  s.bytesIn,
		"bytes_out": s.bytesOut,
	}
}

func (s *SerialService) emitStatus() {
	st := s.GetStatus()
	s.emit(events.EvSerialStatus, map[string]any{
		"state": st["state"], "bytes_in": st["bytes_in"], "bytes_out": st["bytes_out"],
	})
}

// readLoop 读串口 → 33ms 合帧批量推送；读错误即断开（不 panic、不泄漏）。
// done 在退出时关闭，供 Disconnect 等待。
//
// 结构：独立的读 goroutine 只负责把字节搬进 dataCh，本循环用 select 同时等待
// 「新数据 / 合帧定时器 / 停止信号」。若把阻塞 Read 放在本循环里，设备发完最后
// 一帧就安静时，pending 会一直滞留到下一次数据到达（实测可达 15s 以上）。
func (s *SerialService) readLoop(stop, done chan struct{}) {
	defer close(done)
	defer func() {
		s.mu.Lock()
		s.reading = false
		s.mu.Unlock()
	}()

	dataCh := make(chan []byte, 32)
	errCh := make(chan error, 1)

	go func() {
		buf := make([]byte, 4096)
		for {
			// 每次 Read 前先看停止信号：某些实现读不到数据时不返回错误
			// （或返回 0,nil），只靠读错误退出会泄漏这个 goroutine。
			select {
			case <-stop:
				return
			default:
			}
			n, err := s.port.Read(buf)
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
			if n == 0 {
				continue
			}
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			select {
			case dataCh <- chunk:
			case <-stop:
				return
			}
		}
	}()

	var pending []byte
	ticker := time.NewTicker(time.Duration(batchIntervalMs) * time.Millisecond)
	defer ticker.Stop()
	lastStatus := time.Now()

	flush := func() {
		if len(pending) == 0 {
			return
		}
		if em := s.emitter(); em != nil {
			em(events.EvSerialData, map[string]any{
				"hex": strings.ToUpper(hex.EncodeToString(pending)),
				"ts":  time.Now().UnixMilli(),
			})
		}
		pending = pending[:0]
	}

	for {
		select {
		case <-stop:
			flush()
			return
		case <-errCh:
			flush()
			s.mu.Lock()
			s.connected = false
			s.mu.Unlock()
			_ = s.port.Close()
			s.emitStatus()
			return
		case chunk := <-dataCh:
			pending = append(pending, chunk...)
			s.mu.Lock()
			s.bytesIn += uint64(len(chunk))
			s.mu.Unlock()
			if tap := s.rawTap(); tap != nil {
				tap(chunk) // 解析管线旁路：终端显示与帧解析共用同一份原始字节
			}
		case <-ticker.C:
			flush()
			// 流式期间按 1Hz 刷新状态事件，否则前端 RX/TX 计数只在连接/断开时更新
			if time.Since(lastStatus) >= time.Second {
				lastStatus = time.Now()
				s.emitStatus()
			}
		}
	}
}
