// Package lua 协议脚本运行时（PRD §F3）：LState 池 + 沙箱 + 字节码缓存 + 硬超时。
//
// 设计要点：
//   - 池：禁止跨 goroutine 共享 LState，取用/归还全部经池；
//   - 超时：每次调用 SetContext(deadline)，gopher-lua 在 VM 主循环逐指令检查，
//     超时的 LState 直接销毁不归还（被中断的栈不可信）；
//   - 沙箱：只开 base/table/string/math，且把 base 里的 dofile/loadfile/load 等逃逸口置 nil；
//   - 字节码缓存：按脚本名缓存 FunctionProto，所有 LState 共用同一份只读 proto，
//     省去每次建池都重新编译（PRD「预编译字节码缓存」）。
package lua

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	glua "github.com/yuin/gopher-lua"
)

// DefaultPoolSize 池容量。解析是单 goroutine 顺序消费，但 AI 沙箱试跑与
// 未来并行解析会并发取用，预留 4 个。
const DefaultPoolSize = 4

// DefaultTimeout 单次调用硬上限（PRD §F3）。
const DefaultTimeout = 5 * time.Second

var (
	// ErrNoScript 未加载脚本。
	ErrNoScript = errors.New("lua: no script loaded")
	// ErrClosed 运行时已关闭。
	ErrClosed = errors.New("lua: runtime closed")
	// ErrTimeout 脚本执行超时。
	ErrTimeout = errors.New("lua: script timeout")
)

// ChartPoint emit_chart 产生的一个数据点。
type ChartPoint struct {
	Channel string
	Value   float64
}

// Sinks 脚本对外副作用。由壳层注入（emit_chart → 图表批量、send_bytes → 串口、log → 控制台）。
type Sinks struct {
	Send func(data []byte) error
	Log  func(level, msg string)
}

// Options 运行时配置。
type Options struct {
	PoolSize int
	Timeout  time.Duration
	Sinks    Sinks
}

// Runtime 脚本运行时。
type Runtime struct {
	mu       sync.Mutex
	poolSize int
	timeout  time.Duration
	sinks    Sinks

	scriptName string
	source     []byte
	proto      *glua.FunctionProto
	cacheGen   uint64 // 每次 LoadScript/Reset 自增，用于拒绝过期 LState

	idle   []*glua.LState
	states map[*glua.LState]uint64 // 存活 state → 创建时的 generation
	closed bool
}

// New 创建运行时。
func New(opts Options) *Runtime {
	if opts.PoolSize <= 0 {
		opts.PoolSize = DefaultPoolSize
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.Sinks.Log == nil {
		opts.Sinks.Log = func(string, string) {}
	}
	return &Runtime{
		poolSize: opts.PoolSize,
		timeout:  opts.Timeout,
		sinks:    opts.Sinks,
		states:   map[*glua.LState]uint64{},
	}
}

// LoadScript 编译并缓存脚本（不落盘），同时丢弃旧 LState（旧脚本的全局状态必须清掉）。
func (r *Runtime) LoadScript(name string, src []byte) error {
	proto, err := compileScript(name, src)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	r.resetLocked()
	r.scriptName = name
	r.source = append([]byte(nil), src...)
	r.proto = proto
	return nil
}

// ActiveScript 返回当前脚本名。
func (r *Runtime) ActiveScript() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.scriptName
}

// CacheSize 已缓存字节码条目数（验收「工程切换后残留字节码 = 0」）。
func (r *Runtime) CacheSize() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proto == nil {
		return 0
	}
	return 1
}

// Reset 清空字节码缓存与池中 LState（工程切换/关闭时调用）。
// 正在执行中的 LState 不在此处强杀：cacheGen 自增后，它们归还时会被识别为过期并销毁。
func (r *Runtime) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resetLocked()
}

func (r *Runtime) resetLocked() {
	for _, L := range r.idle {
		L.Close()
		delete(r.states, L)
	}
	r.idle = nil
	r.scriptName = ""
	r.source = nil
	r.proto = nil
	r.cacheGen++
}

func (r *Runtime) clearLocked() {
	// states 是全部存活 LState 的唯一来源（idle 只是其中可复用的子集），
	// 只遍历它，避免同一 state 被 Close 两次（gopher-lua 二次 Close 会 panic）。
	for L := range r.states {
		L.Close()
	}
	r.states = map[*glua.LState]uint64{}
	r.idle = nil
	r.scriptName = ""
	r.source = nil
	r.proto = nil
}

// Close 释放资源。
func (r *Runtime) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.clearLocked()
	r.closed = true
}

// acquire 取一个已加载脚本的 LState。
func (r *Runtime) acquire() (*glua.LState, uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, 0, ErrClosed
	}
	if r.proto == nil {
		return nil, 0, ErrNoScript
	}
	gen := r.cacheGen
	for len(r.idle) > 0 {
		L := r.idle[len(r.idle)-1]
		r.idle = r.idle[:len(r.idle)-1]
		if r.states[L] == gen {
			return L, gen, nil
		}
		// 旧代 state（脚本已换）：直接销毁
		L.Close()
		delete(r.states, L)
	}
	L, err := r.newStateLocked(gen)
	if err != nil {
		return nil, 0, err
	}
	return L, gen, nil
}

// release 归还 LState；gen 不匹配则销毁。
func (r *Runtime) release(L *glua.LState, gen uint64) {
	L.SetTop(0)
	L.RemoveContext()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.states[L] != gen || gen != r.cacheGen || len(r.idle) >= r.poolSize {
		L.Close()
		delete(r.states, L)
		return
	}
	r.idle = append(r.idle, L)
}

// discard 执行出错/超时：LState 不再归还。
func (r *Runtime) discard(L *glua.LState) {
	r.mu.Lock()
	delete(r.states, L)
	r.mu.Unlock()
	L.Close()
}

var collectorKey = struct{ name string }{"chart-collector"}

type chartCollector struct {
	points []ChartPoint
}

// newStateLocked 创建沙箱内 LState 并执行脚本顶层（定义 on_frame 等全局函数）。
func (r *Runtime) newStateLocked(gen uint64) (*glua.LState, error) {
	L := newSandboxedState(r.sinks)
	fn := L.NewFunctionFromProto(r.proto)
	L.Push(fn)
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	L.SetContext(ctx)
	err := L.PCall(0, glua.MultRet, nil)
	cancel()
	L.RemoveContext()
	L.SetTop(0)
	if err != nil {
		L.Close()
		return nil, fmt.Errorf("lua: run %s: %w", r.scriptName, err)
	}
	r.states[L] = gen
	return L, nil
}

// newSandboxedState 只开 base/table/string/math，并封死文件/加载类逃逸口。
func newSandboxedState(sinks Sinks) *glua.LState {
	L := glua.NewState(glua.Options{SkipOpenLibs: true})
	libs := []struct {
		name string
		fn   glua.LGFunction
	}{
		{glua.BaseLibName, glua.OpenBase},
		{glua.TabLibName, glua.OpenTable},
		{glua.StringLibName, glua.OpenString},
		{glua.MathLibName, glua.OpenMath},
	}
	for _, lib := range libs {
		L.Push(L.NewFunction(lib.fn))
		L.Push(glua.LString(lib.name))
		L.Call(1, 0)
	}
	// OpenBase 会注册这些逃逸口，逐个抹掉；package/io/os/debug 根本没开。
	for _, name := range []string{
		"dofile", "loadfile", "load", "loadstring", "require", "module",
		"newproxy", "collectgarbage",
	} {
		L.SetGlobal(name, glua.LNil)
	}
	registerAPI(L, sinks)
	return L
}

// CallOnFrame 调用脚本入口 on_frame(raw)，返回字段表与 emit_chart 收集到的点。
func (r *Runtime) CallOnFrame(raw []byte) (map[string]any, []ChartPoint, error) {
	L, gen, err := r.acquire()
	if err != nil {
		return nil, nil, err
	}
	fn := L.GetGlobal("on_frame")
	if fn == glua.LNil {
		r.release(L, gen)
		return nil, nil, fmt.Errorf("lua: script has no on_frame")
	}

	collector := &chartCollector{}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	ctx = context.WithValue(ctx, collectorKey, collector)
	defer cancel()
	L.SetContext(ctx)

	L.Push(fn)
	L.Push(glua.LString(string(raw)))
	callErr := L.PCall(1, glua.MultRet, nil)
	if callErr != nil {
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
		r.discard(L)
		if timedOut {
			return nil, collector.points, fmt.Errorf("%w: %v", ErrTimeout, callErr)
		}
		return nil, collector.points, fmt.Errorf("lua: on_frame: %w", callErr)
	}

	var out map[string]any
	if n := L.GetTop(); n > 0 {
		if tbl, ok := L.Get(-1).(*glua.LTable); ok {
			out = tableToMap(tbl)
		}
	}
	r.release(L, gen)
	return out, collector.points, nil
}

// CallEncodeCommand 调用 encode_command(cmd, params) → string。
func (r *Runtime) CallEncodeCommand(cmd string, params map[string]any) ([]byte, error) {
	L, gen, err := r.acquire()
	if err != nil {
		return nil, err
	}
	fn := L.GetGlobal("encode_command")
	if fn == glua.LNil {
		r.release(L, gen)
		return nil, fmt.Errorf("lua: script has no encode_command")
	}
	collector := &chartCollector{}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	ctx = context.WithValue(ctx, collectorKey, collector)
	defer cancel()
	L.SetContext(ctx)

	L.Push(fn)
	L.Push(glua.LString(cmd))
	L.Push(mapToTable(L, params))
	callErr := L.PCall(2, 1, nil)
	if callErr != nil {
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
		r.discard(L)
		if timedOut {
			return nil, fmt.Errorf("%w: %v", ErrTimeout, callErr)
		}
		return nil, fmt.Errorf("lua: encode_command: %w", callErr)
	}
	ret := L.Get(-1)
	out, ok := luaValueToBytes(ret)
	r.release(L, gen)
	if !ok {
		return nil, fmt.Errorf("lua: encode_command must return string")
	}
	return out, nil
}

// HasDetectFrame 脚本是否提供自定义切帧函数。
func (r *Runtime) HasDetectFrame() bool {
	L, gen, err := r.acquire()
	if err != nil {
		return false
	}
	defer r.release(L, gen)
	return L.GetGlobal("detect_frame") != glua.LNil
}

// CallDetectFrame 调用 detect_frame(buf) → 边界索引（0 表示「数据不足，继续等」）。
func (r *Runtime) CallDetectFrame(buf []byte) (int, error) {
	L, gen, err := r.acquire()
	if err != nil {
		return 0, err
	}
	fn := L.GetGlobal("detect_frame")
	if fn == glua.LNil {
		r.release(L, gen)
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	L.SetContext(ctx)

	L.Push(fn)
	L.Push(glua.LString(string(buf)))
	callErr := L.PCall(1, 1, nil)
	if callErr != nil {
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
		r.discard(L)
		if timedOut {
			return 0, fmt.Errorf("%w: %v", ErrTimeout, callErr)
		}
		return 0, fmt.Errorf("lua: detect_frame: %w", callErr)
	}
	n := glua.LVAsNumber(L.Get(-1))
	r.release(L, gen)
	return int(n), nil
}

// compileScript 用一次性 LState 编译出 FunctionProto（只读，可跨 state 复用）。
// name 作为 chunk 名，脚本报错时行号/文件名为用户可读的脚本名。
func compileScript(name string, src []byte) (*glua.FunctionProto, error) {
	tmp := glua.NewState(glua.Options{SkipOpenLibs: true})
	defer tmp.Close()
	fn, err := tmp.Load(strings.NewReader(string(src)), name)
	if err != nil {
		return nil, fmt.Errorf("lua: compile %s: %w", name, err)
	}
	return fn.Proto, nil
}
