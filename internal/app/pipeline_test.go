package app

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"seriallens/internal/charts"
	"seriallens/internal/events"
	"seriallens/internal/lua"
	"seriallens/internal/parser"
	"seriallens/internal/project"
	"seriallens/internal/scripts"
)

// newWiredServices 手工装配与 main.go 等价的管线，但串口换成 mockPort：
//
//	mockPort.Read → SerialService.readLoop → tap(Ingest) → framer → Lua on_frame → 事件
//
// 这条链路里 SerialService 的 rawTap 接线此前没有测试覆盖。
func newWiredServices(t *testing.T) (*Services, *mockPort, *eventLog) {
	t.Helper()
	log := &eventLog{}
	port := newMockPort()
	s := &Services{}
	s.Serial = NewSerialService(port, log.emit)
	s.runtime = lua.New(lua.Options{PoolSize: 2, Timeout: time.Second})
	s.pipe = parser.New(parser.Options{
		Runtime: s.runtime,
		Emit:    log.emit,
		Idle:    20 * time.Millisecond,
	})
	s.Serial.SetRawTap(s.pipe.Ingest)
	s.charts = charts.NewService(log.emit)
	s.scripts = scripts.New(log.emit)
	s.scripts.SetLoader(func(name string, src []byte) error {
		return s.pipe.LoadScriptSource(name, src)
	})
	s.Project = newProjectService(s)
	s.Project.svc.SetEmitter(log.emit) // 生产由 ProjectService.ServiceStartup 注入
	s.Project.svc.SetHooks(&switchHooks{s: s})
	s.pipe.Start()

	t.Cleanup(func() {
		_ = s.Serial.Disconnect()
		s.scripts.Close()
		s.charts.Close()
		s.pipe.Stop()
		s.runtime.Close()
	})
	return s, port, log
}

type eventLog struct {
	mu     sync.Mutex
	events []string
	frames []map[string]any
}

func (l *eventLog) emit(name string, payload map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, name)
	if name == events.EvFrameParsed {
		l.frames = append(l.frames, payload)
	}
}

func (l *eventLog) waitFrame(t *testing.T, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		if len(l.frames) > 0 {
			f := l.frames[0]
			l.mu.Unlock()
			return f
		}
		l.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	l.mu.Lock()
	names := append([]string(nil), l.events...)
	l.mu.Unlock()
	t.Fatalf("no frame:parsed within %v; events=%v", timeout, names)
	return nil
}

func (l *eventLog) seen(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.events {
		if e == name {
			return true
		}
	}
	return false
}

const modbusE2EScript = `
function on_frame(raw)
    if #raw < 5 then return nil end
    local crc = crc16_modbus(string.sub(raw, 1, #raw - 2))
    local lo = struct_uint8(raw, #raw - 2)
    local hi = struct_uint8(raw, #raw - 1)
    if band(crc, 0xFF) ~= lo or band(brshift(crc, 8), 0xFF) ~= hi then
        return { crc_ok = false }
    end
    emit_chart("reg1", struct_uint16_be(raw, 3))
    return { addr = struct_uint8(raw, 0), crc_ok = true }
end
`

// M2 端到端：打开工程 → 自动加载 active_script → 串口读到的字节 → frame:parsed。
func TestProjectOpenWiresSerialTapToLua(t *testing.T) {
	s, port, log := newWiredServices(t)

	dir := t.TempDir()
	meta, err := project.Scaffold(dir, "modbus-e2e", "")
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", meta.ActiveScript), []byte(modbusE2EScript), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Project.svc.Open(dir); err != nil {
		t.Fatalf("open project: %v", err)
	}
	if !log.seen(events.EvScriptReloaded) {
		t.Fatal("project open should load active_script (script:reloaded not seen)")
	}
	if got := s.pipe.Stats()["script"]; got != meta.ActiveScript {
		t.Fatalf("active script = %v, want %v", got, meta.ActiveScript)
	}

	if err := s.Serial.Connect(map[string]any{
		"port": "MOCK", "baudrate": 115200, "data_bits": 8,
		"stop_bits": 1, "parity": "N", "flow_control": "none",
	}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	frame := []byte{0x01, 0x03, 0x02, 0x00, 0x2A}
	crc := lua.CRC16Modbus(frame)
	frame = append(frame, byte(crc&0xFF), byte(crc>>8))
	port.readCh <- frame

	got := log.waitFrame(t, 2*time.Second)
	fields, _ := got["fields"].(map[string]any)
	if fields["crc_ok"] != true || fields["addr"] != 1.0 {
		t.Fatalf("fields = %#v", fields)
	}
}

// 切换工程必须真的把串口断开（PRD §3.8 第一步），且旧脚本不再解析。
func TestProjectSwitchDisconnectsSerialAndClearsBytecode(t *testing.T) {
	s, port, log := newWiredServices(t)

	dirA := t.TempDir()
	dirB := t.TempDir()
	for _, d := range []string{dirA, dirB} {
		meta, err := project.Scaffold(d, filepath.Base(d), "")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "scripts", meta.ActiveScript), []byte(modbusE2EScript), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Project.svc.Open(dirA); err != nil {
		t.Fatal(err)
	}
	if err := s.Serial.Connect(map[string]any{
		"port": "MOCK", "baudrate": 115200, "data_bits": 8,
		"stop_bits": 1, "parity": "N", "flow_control": "none",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Project.svc.Open(dirB); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if state := s.Serial.GetStatus()["state"]; state != "disconnected" {
		t.Fatalf("serial state after switch = %v, want disconnected", state)
	}
	if got := s.pipe.Stats()["bytecode"]; got != 1 {
		t.Fatalf("bytecode after switch = %v, want 1（新脚本已编译）", got)
	}
	if got := s.pipe.Stats()["script"]; got == "" {
		t.Fatal("script not loaded after switch")
	}

	// 关闭工程后必须彻底清空
	if err := s.Project.svc.Close(); err != nil {
		t.Fatal(err)
	}
	if got := s.pipe.Stats()["bytecode"]; got != 0 {
		t.Fatalf("bytecode after close = %v, want 0（PRD §8 残留字节码 = 0）", got)
	}
	if !log.seen(events.EvProjectClosed) {
		t.Fatal("project:closed not emitted")
	}
	_ = port
}
