package lua

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := New(Options{PoolSize: 2, Timeout: 2 * time.Second})
	t.Cleanup(rt.Close)
	return rt
}

func TestOnFrameParsesAndCollectsChart(t *testing.T) {
	rt := newTestRuntime(t)
	src := `
function on_frame(raw)
    local temp = struct_int16_be(raw, 1) / 10
    local hum = struct_uint8(raw, 3)
    emit_chart("temperature", temp)
    emit_chart("humidity", hum)
    return { temp = temp, humidity = hum, ok = true, tags = { "a", "b" } }
end
`
	if err := rt.LoadScript("main.lua", []byte(src)); err != nil {
		t.Fatalf("LoadScript: %v", err)
	}
	// 帧头 0xAA + int16(235) + uint8(60)
	raw := []byte{0xAA, 0x00, 0xEB, 0x3C}
	fields, points, err := rt.CallOnFrame(raw)
	if err != nil {
		t.Fatalf("CallOnFrame: %v", err)
	}
	if got := fields["temp"]; got != 23.5 {
		t.Fatalf("temp = %v, want 23.5", got)
	}
	if got := fields["humidity"]; got != 60.0 {
		t.Fatalf("humidity = %v, want 60", got)
	}
	if got := fields["ok"]; got != true {
		t.Fatalf("ok = %v, want true", got)
	}
	if tags, ok := fields["tags"].([]any); !ok || len(tags) != 2 {
		t.Fatalf("tags = %#v, want 2-element array", fields["tags"])
	}
	if len(points) != 2 || points[0].Channel != "temperature" || points[1].Value != 60 {
		t.Fatalf("points = %#v", points)
	}
}

func TestOnFrameMissingEntry(t *testing.T) {
	rt := newTestRuntime(t)
	if err := rt.LoadScript("noop.lua", []byte(`local x = 1`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := rt.CallOnFrame([]byte{1}); err == nil || !strings.Contains(err.Error(), "on_frame") {
		t.Fatalf("err = %v, want missing on_frame", err)
	}
}

// 沙箱：文件/网络/进程访问与动态加载必须不可用。
func TestSandboxBlocksEscapes(t *testing.T) {
	rt := newTestRuntime(t)
	src := `
function probe()
    return { os_lib = (os ~= nil), io_lib = (io ~= nil), package_lib = (package ~= nil),
             debug_lib = (debug ~= nil), dofile_fn = (dofile ~= nil), loadfile_fn = (loadfile ~= nil),
             load_fn = (load ~= nil), require_fn = (require ~= nil) }
end

function on_frame(raw)
    return probe()
end
`
	if err := rt.LoadScript("sandbox.lua", []byte(src)); err != nil {
		t.Fatal(err)
	}
	fields, _, err := rt.CallOnFrame(nil)
	if err != nil {
		t.Fatalf("CallOnFrame: %v", err)
	}
	for _, k := range []string{"os_lib", "io_lib", "package_lib", "debug_lib", "dofile_fn", "loadfile_fn", "load_fn", "require_fn"} {
		if v, ok := fields[k].(bool); !ok || v {
			t.Fatalf("%s should be unavailable, got %#v", k, fields[k])
		}
	}
}

// 死循环必须被硬超时打断，且被打断的 state 不归还池中。
func TestTimeoutDiscardsState(t *testing.T) {
	rt := New(Options{PoolSize: 1, Timeout: 150 * time.Millisecond})
	t.Cleanup(rt.Close)

	src := `
function on_frame(raw)
    if raw == "loop" then
        while true do end
    end
    return { n = #raw }
end
`
	if err := rt.LoadScript("timeout.lua", []byte(src)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, _, err := rt.CallOnFrame([]byte("loop"))
	elapsed := time.Since(start)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("timeout took %v, hook not effective", elapsed)
	}
	// 池中 state 被销毁后必须能新建，运行时自愈
	fields, _, err := rt.CallOnFrame([]byte("ok"))
	if err != nil {
		t.Fatalf("runtime not recoverable: %v", err)
	}
	if fields["n"] != 2.0 {
		t.Fatalf("n = %v, want 2", fields["n"])
	}
}

func TestLoadScriptInvalidatesOldStates(t *testing.T) {
	rt := newTestRuntime(t)
	if err := rt.LoadScript("a.lua", []byte(`function on_frame(raw) return { who = "a" } end`)); err != nil {
		t.Fatal(err)
	}
	fields, _, err := rt.CallOnFrame(nil)
	if err != nil || fields["who"] != "a" {
		t.Fatalf("first script: %v %#v", err, fields)
	}
	if err := rt.LoadScript("b.lua", []byte(`function on_frame(raw) return { who = "b" } end`)); err != nil {
		t.Fatal(err)
	}
	fields, _, err = rt.CallOnFrame(nil)
	if err != nil || fields["who"] != "b" {
		t.Fatalf("second script: %v %#v", err, fields)
	}
	if rt.ActiveScript() != "b.lua" {
		t.Fatalf("ActiveScript = %q", rt.ActiveScript())
	}
}

func TestCompileErrorReportsScriptName(t *testing.T) {
	rt := newTestRuntime(t)
	err := rt.LoadScript("broken.lua", []byte("function on_frame(raw)\n  return {}\n"))
	if err == nil || !strings.Contains(err.Error(), "broken.lua") {
		t.Fatalf("err = %v, want compile error mentioning script name", err)
	}
	if rt.CacheSize() != 0 {
		t.Fatalf("failed compile must not cache bytecode, got %d", rt.CacheSize())
	}
}

// 工程切换验收：Reset 后字节码缓存必须为 0，且不能再调用旧脚本。
func TestResetClearsBytecodeCache(t *testing.T) {
	rt := newTestRuntime(t)
	if err := rt.LoadScript("main.lua", []byte(`function on_frame(raw) return {} end`)); err != nil {
		t.Fatal(err)
	}
	if rt.CacheSize() != 1 {
		t.Fatalf("CacheSize = %d, want 1", rt.CacheSize())
	}
	rt.Reset()
	if rt.CacheSize() != 0 {
		t.Fatalf("after Reset CacheSize = %d, want 0", rt.CacheSize())
	}
	if rt.ActiveScript() != "" {
		t.Fatalf("after Reset ActiveScript = %q", rt.ActiveScript())
	}
	if _, _, err := rt.CallOnFrame(nil); !errors.Is(err, ErrNoScript) {
		t.Fatalf("err = %v, want ErrNoScript", err)
	}
}

func TestEncodeCommandAndDetectFrame(t *testing.T) {
	rt := newTestRuntime(t)
	src := `
function on_frame(raw) return {} end

function encode_command(cmd, params)
    if cmd == "set_temp" then
        local v = params.value
        return string.char(0xAA, band(brshift(v, 8), 0xFF), band(v, 0xFF))
    end
    error("unknown command: " .. tostring(cmd))
end

function detect_frame(buf)
    if #buf < 2 then return 0 end
    return struct_uint8(buf, 0) + 2
end
`
	if err := rt.LoadScript("cmd.lua", []byte(src)); err != nil {
		t.Fatal(err)
	}
	out, err := rt.CallEncodeCommand("set_temp", map[string]any{"value": 0x0102})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(out) != 3 || out[0] != 0xAA || out[1] != 0x01 || out[2] != 0x02 {
		t.Fatalf("out = % X", out)
	}
	if _, err := rt.CallEncodeCommand("nope", nil); err == nil {
		t.Fatal("unknown command should error")
	}
	if !rt.HasDetectFrame() {
		t.Fatal("HasDetectFrame = false")
	}
	n, err := rt.CallDetectFrame([]byte{0x02, 0x00, 0x00, 0x00})
	if err != nil || n != 4 {
		t.Fatalf("detect = %d, %v; want 4", n, err)
	}
	n, err = rt.CallDetectFrame([]byte{0x02})
	if err != nil || n != 0 {
		t.Fatalf("detect short = %d, %v; want 0", n, err)
	}
}

func TestSendBytesSink(t *testing.T) {
	var got []byte
	rt := New(Options{PoolSize: 1, Timeout: time.Second, Sinks: Sinks{
		Send: func(data []byte) error { got = append([]byte(nil), data...); return nil },
	}})
	t.Cleanup(rt.Close)
	src := `
function on_frame(raw)
    send_bytes(string.char(0x01, 0x03))
    return {}
end
`
	if err := rt.LoadScript("tx.lua", []byte(src)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := rt.CallOnFrame(nil); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 0x01 || got[1] != 0x03 {
		t.Fatalf("send_bytes got % X", got)
	}
}

func TestLogSink(t *testing.T) {
	var level, msg string
	rt := New(Options{PoolSize: 1, Timeout: time.Second, Sinks: Sinks{
		Log: func(l, m string) { level, msg = l, m },
	}})
	t.Cleanup(rt.Close)
	if err := rt.LoadScript("log.lua", []byte(`
function on_frame(raw)
    log("hello", "warn")
    return {}
end`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := rt.CallOnFrame(nil); err != nil {
		t.Fatal(err)
	}
	if level != "warn" || msg != "hello" {
		t.Fatalf("log = %q/%q", level, msg)
	}
}

// 并发取用池：禁止跨 goroutine 共享 LState，池必须线程安全（-race 下验证）。
func TestConcurrentOnFrame(t *testing.T) {
	rt := New(Options{PoolSize: 4, Timeout: 2 * time.Second})
	t.Cleanup(rt.Close)
	src := `
function on_frame(raw)
    local acc = 0
    for i = 1, #raw do acc = acc + struct_uint8(raw, i - 1) end
    emit_chart("sum", acc)
    return { sum = acc }
end
`
	if err := rt.LoadScript("concurrent.lua", []byte(src)); err != nil {
		t.Fatal(err)
	}
	const goroutines = 100
	const iterations = 50
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			raw := []byte{byte(id), 1, 2, 3}
			for i := 0; i < iterations; i++ {
				fields, points, err := rt.CallOnFrame(raw)
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d: %w", id, err)
					return
				}
				if fields["sum"] != 6.0+float64(id) {
					errCh <- fmt.Errorf("goroutine %d: sum=%v", id, fields["sum"])
					return
				}
				if len(points) != 1 {
					errCh <- fmt.Errorf("goroutine %d: points=%v", id, points)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

func TestBuiltinChecksums(t *testing.T) {
	// 标准校验向量 "123456789"
	if got := CRC16Modbus([]byte("123456789")); got != 0x4B37 {
		t.Fatalf("crc16_modbus = %#x, want 0x4b37", got)
	}
	if got := CRC16CCITT([]byte("123456789")); got != 0x29B1 {
		t.Fatalf("crc16_ccitt = %#x, want 0x29b1", got)
	}
	var sum byte
	for _, b := range []byte{1, 2, 3} {
		sum += b
	}
	if sum != 6 {
		t.Fatal("sanity")
	}
}

func TestStructReaders(t *testing.T) {
	rt := newTestRuntime(t)
	src := `
function on_frame(raw)
    return {
        i16be = struct_int16_be(raw, 0),
        i16le = struct_int16_le(raw, 0),
        u16be = struct_uint16_be(raw, 0),
        i32be = struct_int32_be(raw, 0),
        f32be = struct_float32_be(raw, 0),
        hex   = bytes_to_hex(raw),
    }
end
`
	if err := rt.LoadScript("struct.lua", []byte(src)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, 0xFFFF0001)
	f, _, err := rt.CallOnFrame(buf)
	if err != nil {
		t.Fatal(err)
	}
	if f["i16be"] != -1.0 {
		t.Fatalf("i16be = %v", f["i16be"])
	}
	if f["u16be"] != 65535.0 {
		t.Fatalf("u16be = %v", f["u16be"])
	}
	if f["hex"] != "FFFF0001" {
		t.Fatalf("hex = %v", f["hex"])
	}
}

// 越界读取必须抛出带上下文的 Lua 错误而不是 panic。
func TestStructOutOfRangeSurfacesError(t *testing.T) {
	rt := newTestRuntime(t)
	if err := rt.LoadScript("oob.lua", []byte(`
function on_frame(raw)
    return { v = struct_int32_be(raw, 1) }
end`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := rt.CallOnFrame([]byte{0x00}); err == nil {
		t.Fatal("out-of-range read should error")
	}
}
