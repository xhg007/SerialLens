// Package parser 解析管线（PRD §7.2）：串口原始字节 → 切帧 → Lua on_frame → 事件。
//
// 数据流：SerialService 的读 goroutine 把每批字节交给 Ingest（非阻塞），
// 本包单 goroutine 消费：framer 切帧 → LuaRuntime.CallOnFrame → 字段表/图表点。
// 图表点按 33ms 合批推送（与串口批量口径一致）。
package parser

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"seriallens/internal/events"
	"seriallens/internal/framer"
	"seriallens/internal/lua"
)

const (
	// ingestBuffer 入站队列长度。解析是微秒级，256 批足够吸抖；
	// 溢出时丢弃并计数（不阻塞串口读，避免一个卡死的脚本拖垮收发）。
	ingestBuffer = 256
	// tickInterval 切帧/合批驱动周期。
	tickInterval = 5 * time.Millisecond
	// chartBatchInterval 图表点合批（PRD ADR-02 的 33ms 口径）。
	chartBatchInterval = 33 * time.Millisecond
	// errorThrottle 脚本错误上报节流，避免每帧刷屏。
	errorThrottle = time.Second
)

// Options 解析管线依赖。
type Options struct {
	Runtime   *lua.Runtime
	Emit      events.Emitter
	Idle      time.Duration
	MaxFrame  int
	QueueSize int
}

// Service 解析管线。
type Service struct {
	rt   *lua.Runtime
	fr   *framer.Framer
	emit events.Emitter
	in   chan []byte

	stop chan struct{}
	done chan struct{}
	once sync.Once

	// frMu 保护 framer：consumeLoop 使用它，而 LoadScript/Clear/Stats 来自其他 goroutine。
	frMu sync.Mutex

	mu         sync.Mutex
	scriptName string
	frames     uint64
	errors     uint64
	dropped    uint64
	lastError  string
	lastErrAt  time.Time

	chartMu   sync.Mutex
	chartBuf  []map[string]any
	lastChart time.Time
}

// New 创建解析管线（不启动）。
func New(opts Options) *Service {
	if opts.Emit == nil {
		opts.Emit = func(string, map[string]any) {}
	}
	size := opts.QueueSize
	if size <= 0 {
		size = ingestBuffer
	}
	return &Service{
		rt:   opts.Runtime,
		fr:   framer.New(opts.Idle, opts.MaxFrame, nil),
		emit: opts.Emit,
		in:   make(chan []byte, size),
	}
}

// Start 启动消费 goroutine。
func (s *Service) Start() {
	s.mu.Lock()
	if s.stop != nil {
		s.mu.Unlock()
		return
	}
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	stop, done := s.stop, s.done
	s.mu.Unlock()
	go s.consumeLoop(stop, done)
}

// Stop 停止消费并等待退出。
func (s *Service) Stop() {
	s.mu.Lock()
	stop, done := s.stop, s.done
	s.stop, s.done = nil, nil
	s.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-done
}

// Ingest 由串口读循环调用：非阻塞入队，队列满则丢弃并计数。
func (s *Service) Ingest(data []byte) {
	if len(data) == 0 {
		return
	}
	buf := make([]byte, len(data))
	copy(buf, data)
	select {
	case s.in <- buf:
	default:
		s.mu.Lock()
		s.dropped++
		s.mu.Unlock()
	}
}

// LoadScript 从工程 scripts/ 目录加载脚本（dir 为空时忽略）。
func (s *Service) LoadScript(projectDir, name string) error {
	if name == "" {
		return fmt.Errorf("parser: empty script name")
	}
	path := filepath.Join(projectDir, "scripts", name)
	// 只允许纯文件名，防止路径逃逸（与 metadata.Validate 同一策略）
	if filepath.Base(name) != name {
		return fmt.Errorf("parser: invalid script name %q", name)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("parser: read %s: %w", path, err)
	}
	return s.LoadScriptSource(name, src)
}

// LoadScriptSource 编译并激活脚本。
func (s *Service) LoadScriptSource(name string, src []byte) error {
	if err := s.rt.LoadScript(name, src); err != nil {
		s.emitError(name, err)
		return err
	}
	s.mu.Lock()
	s.scriptName = name
	s.mu.Unlock()
	var detect framer.DetectFunc
	if s.rt.HasDetectFrame() {
		detect = s.rt.CallDetectFrame
	}
	s.frMu.Lock()
	s.fr.Reset()
	s.fr.SetDetect(detect)
	s.frMu.Unlock()
	s.emit(events.EvScriptReloaded, map[string]any{"name": name})
	return nil
}

// Clear 工程切换/关闭：清空字节码与切帧缓冲（验收要求残留字节码 = 0）。
func (s *Service) Clear() {
	s.rt.Reset()
	s.frMu.Lock()
	s.fr.Reset()
	s.fr.SetDetect(nil)
	s.frMu.Unlock()
	s.mu.Lock()
	s.scriptName = ""
	s.mu.Unlock()
}

// Stats 运行统计（前端状态栏 / 验收用）。
func (s *Service) Stats() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{
		"script":     s.scriptName,
		"frames":     s.frames,
		"errors":     s.errors,
		"dropped":    s.dropped,
		"last_error": s.lastError,
		"bytecode":   s.rt.CacheSize(),
		"pending":    s.framePending(),
		"queue_len":  len(s.in),
		"queue_cap":  cap(s.in),
	}
}

func (s *Service) consumeLoop(stop, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			s.flushCharts()
			return
		case chunk := <-s.in:
			s.feed(chunk)
		case now := <-ticker.C:
			for _, frame := range s.frameFlushIdle(now) {
				s.parseFrame(frame, now)
			}
			if now.Sub(s.chartTime()) >= chartBatchInterval {
				s.flushCharts()
			}
		}
	}
}

func (s *Service) feed(chunk []byte) {
	now := time.Now()
	frames, err := s.frameFeed(chunk, now)
	if err != nil {
		s.emitError(s.script(), err)
	}
	for _, frame := range frames {
		s.parseFrame(frame, now)
	}
}

func (s *Service) frameFeed(chunk []byte, now time.Time) ([][]byte, error) {
	s.frMu.Lock()
	defer s.frMu.Unlock()
	return s.fr.Feed(chunk, now)
}

func (s *Service) frameFlushIdle(now time.Time) [][]byte {
	s.frMu.Lock()
	defer s.frMu.Unlock()
	return s.fr.FlushIdle(now)
}

func (s *Service) framePending() int {
	s.frMu.Lock()
	defer s.frMu.Unlock()
	return s.fr.Pending()
}

func (s *Service) parseFrame(frame []byte, now time.Time) {
	start := time.Now()
	fields, points, err := s.rt.CallOnFrame(frame)
	dur := time.Since(start)
	if err != nil {
		s.emitError(s.script(), err)
		return
	}
	s.mu.Lock()
	s.frames++
	s.mu.Unlock()

	s.emit(events.EvFrameParsed, map[string]any{
		"hex":         strings.ToUpper(hex.EncodeToString(frame)),
		"ts":          now.UnixMilli(),
		"fields":      fields,
		"duration_us": dur.Microseconds(),
	})
	if len(points) > 0 {
		s.appendPoints(points, now)
	}
}

func (s *Service) appendPoints(points []lua.ChartPoint, now time.Time) {
	ts := now.UnixMilli()
	s.chartMu.Lock()
	defer s.chartMu.Unlock()
	for _, p := range points {
		s.chartBuf = append(s.chartBuf, map[string]any{"ch": p.Channel, "v": p.Value, "ts": ts})
	}
	if s.lastChart.IsZero() {
		s.lastChart = now
	}
}

func (s *Service) chartTime() time.Time {
	s.chartMu.Lock()
	defer s.chartMu.Unlock()
	return s.lastChart
}

// flushCharts 推送图表点合批（33ms 一次）。
func (s *Service) flushCharts() {
	s.chartMu.Lock()
	if len(s.chartBuf) == 0 {
		s.chartMu.Unlock()
		return
	}
	batch := s.chartBuf
	s.chartBuf = nil
	s.lastChart = time.Now()
	s.chartMu.Unlock()

	s.emit(events.EvChartPoint, map[string]any{"points": batch})
}

func (s *Service) script() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scriptName
}

// emitError 上报脚本错误（1Hz 节流），计入统计。
func (s *Service) emitError(script string, err error) {
	now := time.Now()
	s.mu.Lock()
	s.errors++
	s.lastError = err.Error()
	throttled := now.Sub(s.lastErrAt) < errorThrottle
	if !throttled {
		s.lastErrAt = now
	}
	s.mu.Unlock()

	s.emit(events.EvScriptLog, map[string]any{
		"level": "error", "msg": err.Error(), "ts": now.UnixMilli(),
	})
	if throttled {
		return
	}
	s.emit(events.EvScriptError, map[string]any{
		"script": script,
		"error":  err.Error(),
		"line":   errorLine(err),
	})
}

// errorLine 从 gopher-lua 错误文本里提取行号（形如 "main.lua:12: ..."）。
func errorLine(err error) int {
	msg := err.Error()
	for i := 0; i < len(msg); i++ {
		if msg[i] != ':' {
			continue
		}
		j := i + 1
		n := 0
		for j < len(msg) && msg[j] >= '0' && msg[j] <= '9' {
			n = n*10 + int(msg[j]-'0')
			j++
		}
		if j > i+1 && j < len(msg) && msg[j] == ':' {
			return n
		}
	}
	return 0
}
