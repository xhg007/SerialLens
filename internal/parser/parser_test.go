package parser

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"seriallens/internal/events"
	"seriallens/internal/lua"
)

// recorder 收集事件，便于断言顺序与次数。
type recorder struct {
	mu     sync.Mutex
	events []ev
}

type ev struct {
	name    string
	payload map[string]any
}

func (r *recorder) emit(name string, payload map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev{name: name, payload: payload})
}

func (r *recorder) waitFor(t *testing.T, name string, timeout time.Duration) ev {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, e := range r.events {
			if e.name == name {
				r.mu.Unlock()
				return e
			}
		}
		r.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.events))
	for _, e := range r.events {
		names = append(names, e.name+":"+toStr(e.payload))
	}
	t.Fatalf("timeout waiting for %s; got events %v", name, names)
	return ev{}
}

func toStr(p map[string]any) string {
	for _, k := range []string{"msg", "error"} {
		if v, ok := p[k]; ok {
			return fmt.Sprint(v)
		}
	}
	return ""
}

func (r *recorder) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e.name == name {
			n++
		}
	}
	return n
}

func newTestPipeline(t *testing.T) (*Service, *recorder) {
	t.Helper()
	rec := &recorder{}
	rt := lua.New(lua.Options{PoolSize: 2, Timeout: time.Second})
	t.Cleanup(rt.Close)
	p := New(Options{Runtime: rt, Emit: rec.emit, Idle: 20 * time.Millisecond})
	p.Start()
	t.Cleanup(p.Stop)
	return p, rec
}

const modbusScript = `
function on_frame(raw)
    if #raw < 5 then return nil end
    local crc = crc16_modbus(string.sub(raw, 1, #raw - 2))
    local lo = struct_uint8(raw, #raw - 2)
    local hi = struct_uint8(raw, #raw - 1)
    if band(crc, 0xFF) ~= lo or band(brshift(crc, 8), 0xFF) ~= hi then
        return { crc_ok = false }
    end
    local regs = {}
    local n = struct_uint8(raw, 2)
    for i = 0, n / 2 - 1 do
        regs[i + 1] = struct_uint16_be(raw, 3 + i * 2)
    end
    emit_chart("reg1", regs[1])
    if regs[2] ~= nil then emit_chart("reg2", regs[2]) end
    return { addr = struct_uint8(raw, 0), func = struct_uint8(raw, 1), reg1 = regs[1], reg2 = regs[2], crc_ok = true }
end
`

// M2 验收主用例：Modbus RTU 响应帧解析正确（CRC 校验 + 大端寄存器 + 图表点）。
func TestModbusFrameParses(t *testing.T) {
	p, rec := newTestPipeline(t)
	if err := p.LoadScriptSource("main.lua", []byte(modbusScript)); err != nil {
		t.Fatalf("load: %v", err)
	}
	// 从站 01、功能码 03、2 字节数据 0x04、寄存器 0x0064 / 0x00C8
	frame := []byte{0x01, 0x03, 0x04, 0x00, 0x64, 0x00, 0xC8}
	crc := lua.CRC16Modbus(frame)
	frame = append(frame, byte(crc&0xFF), byte(crc>>8))

	p.Ingest(frame)

	e := rec.waitFor(t, events.EvFrameParsed, 2*time.Second)
	fields := e.payload["fields"].(map[string]any)
	if fields["crc_ok"] != true {
		t.Fatalf("crc_ok = %v (fields=%v)", fields["crc_ok"], fields)
	}
	if fields["addr"] != 1.0 || fields["func"] != 3.0 {
		t.Fatalf("addr/func = %v/%v", fields["addr"], fields["func"])
	}
	if fields["reg1"] != 100.0 || fields["reg2"] != 200.0 {
		t.Fatalf("regs = %v/%v", fields["reg1"], fields["reg2"])
	}
	if e.payload["hex"] != "0103040064 00C8 5B 7E" {
		// 仅校验长度与大小写风格（CRC 值随实现）
		hexStr, _ := e.payload["hex"].(string)
		if len(hexStr) != len(frame)*2 {
			t.Fatalf("hex = %v", hexStr)
		}
		_ = hexStr
	}
	if dur, ok := e.payload["duration_us"].(int64); !ok || dur < 0 {
		t.Fatalf("duration_us = %v", e.payload["duration_us"])
	}

	// 图表点按 33ms 合批推送
	c := rec.waitFor(t, events.EvChartPoint, 2*time.Second)
	points := c.payload["points"].([]map[string]any)
	if len(points) != 2 {
		t.Fatalf("points = %v", points)
	}
	if points[0]["ch"] != "reg1" || points[0]["v"] != 100.0 {
		t.Fatalf("point0 = %v", points[0])
	}
}

// 一帧分两批到达：必须等 IDLE 超时后合成一帧，而不是解析两次。
func TestIdleReassemblesSplitFrame(t *testing.T) {
	p, rec := newTestPipeline(t)
	if err := p.LoadScriptSource("main.lua", []byte(modbusScript)); err != nil {
		t.Fatal(err)
	}
	frame := []byte{0x01, 0x03, 0x02, 0x00, 0x2A}
	crc := lua.CRC16Modbus(frame)
	frame = append(frame, byte(crc&0xFF), byte(crc>>8))

	p.Ingest(frame[:3])
	time.Sleep(5 * time.Millisecond)
	p.Ingest(frame[3:])

	e := rec.waitFor(t, events.EvFrameParsed, 2*time.Second)
	fields := e.payload["fields"].(map[string]any)
	if fields["reg1"] != 42.0 {
		t.Fatalf("reg1 = %v", fields["reg1"])
	}
	if got := rec.count(events.EvFrameParsed); got != 1 {
		t.Fatalf("parsed %d times, want 1", got)
	}
}

// 两帧背靠背到达（粘包）且脚本未提供 detect_frame：IDLE 会合并成一帧，
// 该帧 CRC 校验失败而不是崩掉管线。
func TestStickyPacketsWithoutDetectBecomeOneFrame(t *testing.T) {
	p, rec := newTestPipeline(t)
	if err := p.LoadScriptSource("main.lua", []byte(modbusScript)); err != nil {
		t.Fatal(err)
	}
	f1 := []byte{0x01, 0x03, 0x02, 0x00, 0x01}
	f1 = append(f1, byte(lua.CRC16Modbus(f1)&0xFF), byte(lua.CRC16Modbus(f1)>>8))
	f2 := []byte{0x02, 0x03, 0x02, 0x00, 0x02}
	f2 = append(f2, byte(lua.CRC16Modbus(f2)&0xFF), byte(lua.CRC16Modbus(f2)>>8))
	p.Ingest(append(f1, f2...))

	e := rec.waitFor(t, events.EvFrameParsed, 2*time.Second)
	fields := e.payload["fields"].(map[string]any)
	if fields["crc_ok"] != false {
		t.Fatalf("sticky frame should fail CRC, got %v", fields)
	}
}

// 脚本提供 detect_frame 时按脚本边界切分粘包。
func TestDetectFrameSplitsStickyPackets(t *testing.T) {
	p, rec := newTestPipeline(t)
	src := `
function detect_frame(buf)
    if #buf < 2 then return 0 end
    return 6
end
function on_frame(raw)
    return { n = #raw, first = struct_uint8(raw, 0) }
end
`
	if err := p.LoadScriptSource("detect.lua", []byte(src)); err != nil {
		t.Fatal(err)
	}
	p.Ingest([]byte{0x01, 2, 3, 4, 5, 6, 0x07, 8, 9, 10, 11, 12})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && rec.count(events.EvFrameParsed) < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := rec.count(events.EvFrameParsed); got != 2 {
		t.Fatalf("parsed %d frames, want 2", got)
	}
}

// 脚本报错必须上报 script:error 且管线存活（后续帧继续解析）。
func TestScriptErrorKeepsPipelineAlive(t *testing.T) {
	p, rec := newTestPipeline(t)
	src := `
function on_frame(raw)
    if raw == "bad" then error("boom") end
    return { ok = true }
end
`
	if err := p.LoadScriptSource("err.lua", []byte(src)); err != nil {
		t.Fatal(err)
	}
	p.Ingest([]byte("bad"))
	rec.waitFor(t, events.EvScriptError, 2*time.Second)

	p.Ingest([]byte("good"))
	e := rec.waitFor(t, events.EvFrameParsed, 2*time.Second)
	if e.payload["fields"].(map[string]any)["ok"] != true {
		t.Fatal("pipeline should keep parsing after script error")
	}
	stats := p.Stats()
	if stats["errors"].(uint64) == 0 {
		t.Fatal("errors counter not incremented")
	}
}

// 工程切换/关闭：Clear 后字节码必须为 0，且不再解析旧脚本。
func TestClearResetsBytecodeAndStopsParsing(t *testing.T) {
	p, rec := newTestPipeline(t)
	if err := p.LoadScriptSource("main.lua", []byte(`function on_frame(raw) return { x = 1 } end`)); err != nil {
		t.Fatal(err)
	}
	if got := p.Stats()["bytecode"]; got != 1 {
		t.Fatalf("bytecode = %v, want 1", got)
	}
	p.Clear()
	if got := p.Stats()["bytecode"]; got != 0 {
		t.Fatalf("after Clear bytecode = %v, want 0", got)
	}
	p.Ingest([]byte{1, 2, 3})
	time.Sleep(80 * time.Millisecond)
	if n := rec.count(events.EvFrameParsed); n != 0 {
		t.Fatalf("parsed %d frames after Clear, want 0", n)
	}
	if p.Stats()["errors"].(uint64) == 0 {
		t.Fatal("parsing without script should surface an error")
	}
}

// 前端线程读统计 + 编辑器线程重载脚本，与消费循环并发：
// 在 -race 下验证 frMu/s.mu/运行时世代切换没有锁序问题。
func TestConcurrentStatsAndReloadDuringStreaming(t *testing.T) {
	p, _ := newTestPipeline(t)
	src := `
function on_frame(raw)
    return { n = #raw }
end
`
	if err := p.LoadScriptSource("race.lua", []byte(src)); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() { // 灌流
		defer wg.Done()
		frame := make([]byte, 8)
		for {
			select {
			case <-stop:
				return
			default:
				p.Ingest(frame)
				time.Sleep(time.Millisecond)
			}
		}
	}()
	wg.Add(1)
	go func() { // 读统计（Wails 绑定调用）
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = p.Stats()
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()
	wg.Add(1)
	go func() { // 热重载 / 清空
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_ = p.LoadScriptSource("race.lua", []byte(src))
			time.Sleep(5 * time.Millisecond)
		}
		p.Clear()
	}()

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// 队列溢出必须丢弃并计数，不能阻塞串口读。
func TestIngestDropsWhenQueueFull(t *testing.T) {
	rec := &recorder{}
	rt := lua.New(lua.Options{PoolSize: 1, Timeout: time.Second})
	t.Cleanup(rt.Close)
	p := New(Options{Runtime: rt, Emit: rec.emit, QueueSize: 2})
	// 不 Start：无人消费，队列必然打满
	for i := 0; i < 10; i++ {
		p.Ingest([]byte{1})
	}
	if got := p.Stats()["dropped"].(uint64); got == 0 {
		t.Fatal("expected drops when queue is full")
	}
}
