package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"seriallens/internal/lua"
	"seriallens/internal/project"
)

// 保存即生效（PRD §2.2 用例 3 / §F4）：Ctrl+S 后下一条串口帧就用新逻辑解析。
// 验收口径「< 100ms」在这里卡住：保存→装载必须远低于一帧的往返。
func TestSaveActiveScriptTakesEffectImmediately(t *testing.T) {
	s, port, log, dir := newEditorFixture(t, `
function on_frame(raw)
    return { a = 1 }
end
`)

	if err := connectMock(t, s); err != nil {
		t.Fatal(err)
	}
	port.readCh <- []byte{0x01, 0x02}
	if f := waitFields(t, log, func(m map[string]any) bool { return m["a"] == 1.0 }, 2*time.Second); f == nil {
		t.Fatal("old script never produced a=1")
	}

	editor := &EditorAPI{s: s}
	res, err := editor.SaveScript("main.lua", `
function on_frame(raw)
    return { b = 2 }
end
`)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !res.OK {
		t.Fatalf("save not ok: %+v", res)
	}
	if res.ElapsedMS >= 100 {
		t.Fatalf("save→load took %dms, PRD requires < 100ms", res.ElapsedMS)
	}

	port.readCh <- []byte{0x03, 0x04}
	if f := waitFields(t, log, func(m map[string]any) bool { return m["b"] == 2.0 }, 2*time.Second); f == nil {
		t.Fatal("new script did not take effect on the next frame")
	}
	if onDisk, _ := os.ReadFile(filepath.Join(dir, "scripts", "main.lua")); len(onDisk) == 0 {
		t.Fatal("script not written")
	}
}

// 语法错误：编辑器拿回行号，磁盘保留用户编辑，但**旧脚本继续解析**（不能断流）。
func TestSaveBrokenScriptKeepsParsingWithOldLogic(t *testing.T) {
	s, port, log, _ := newEditorFixture(t, `
function on_frame(raw)
    return { ok = 1 }
end
`)
	if err := connectMock(t, s); err != nil {
		t.Fatal(err)
	}

	editor := &EditorAPI{s: s}
	res, err := editor.SaveScript("main.lua", "function on_frame(raw)\n  local x = +\n  return x\nend\n")
	if err != nil {
		t.Fatalf("save should not fail the RPC: %v", err)
	}
	if res.OK || res.Diagnostic == nil {
		t.Fatalf("expected diagnostic, got %+v", res)
	}
	if res.Diagnostic.Line != 2 {
		t.Fatalf("diagnostic line = %d, want 2", res.Diagnostic.Line)
	}
	if got := s.pipe.Stats()["script"]; got != "main.lua" {
		t.Fatalf("active script changed: %v", got)
	}

	port.readCh <- []byte{0x09}
	if f := waitFields(t, log, func(m map[string]any) bool { return m["ok"] == 1.0 }, 2*time.Second); f == nil {
		t.Fatal("old script stopped parsing after a broken save")
	}
}

// 试运行：一次性 Runtime，不换在线脚本、不允许写串口。
func TestTestRunScriptDoesNotTouchLiveState(t *testing.T) {
	s, _, _, _ := newEditorFixture(t, `
function on_frame(raw)
    return { live = 1 }
end
`)
	editor := &EditorAPI{s: s}

	got, err := editor.TestRunScript(`
function on_frame(raw)
    emit_chart("t", struct_uint8(raw, 0))
    return { test = struct_uint8(raw, 0) }
end
`, "2A")
	if err != nil {
		t.Fatal(err)
	}
	if got["ok"] != true {
		t.Fatalf("test run failed: %+v", got)
	}
	fields, _ := got["fields"].(map[string]any)
	if fields["test"] != 42.0 {
		t.Fatalf("fields = %#v", fields)
	}
	if pts, _ := got["points"].([]map[string]any); len(pts) != 1 || pts[0]["ch"] != "t" {
		t.Fatalf("points = %#v", got["points"])
	}
	if got := s.pipe.Stats()["script"]; got != "main.lua" {
		t.Fatalf("test run must not replace the live script, got %v", got)
	}

	// send_bytes 在试运行里必须被拦住（沙箱试跑不得触达硬件）
	bad, err := editor.TestRunScript(`
function on_frame(raw)
    send_bytes(string.char(0xFF))
    return {}
end
`, "00")
	if err != nil {
		t.Fatal(err)
	}
	if bad["ok"] != false {
		t.Fatalf("send_bytes should fail in test run: %+v", bad)
	}
	if d, _ := bad["diagnostic"].(lua.Diagnostic); d.Message == "" || d.Line != 3 {
		t.Fatalf("missing diagnostic: %+v", bad)
	}
}

// 语法错误的试运行也要带行号给编辑器。
func TestTestRunReportsDiagnosticLine(t *testing.T) {
	s, _, _, _ := newEditorFixture(t, "function on_frame() end\n")
	editor := &EditorAPI{s: s}
	got, err := editor.TestRunScript("local a = 1\nlocal b = +\nreturn b\n", "00")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := got["diagnostic"].(lua.Diagnostic)
	if got["ok"] != false || d.Message == "" {
		t.Fatalf("expected diagnostic: %+v", got)
	}
	if d.Line != 2 {
		t.Fatalf("diagnostic line = %d, want 2 (%+v)", d.Line, d)
	}
}

// 重命名活动脚本：project.json 与解析管线都要跟着改，否则下次打开工程指向旧名字。
func TestRenameActiveScriptUpdatesProjectMeta(t *testing.T) {
	s, _, _, dir := newEditorFixture(t, "function on_frame() end\n")
	editor := &EditorAPI{s: s}

	if err := editor.RenameScript("main.lua", "proto.lua"); err != nil {
		t.Fatal(err)
	}
	meta, err := project.LoadMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ActiveScript != "proto.lua" {
		t.Fatalf("project.json active_script = %q", meta.ActiveScript)
	}
	if got := s.scripts.Active(); got != "proto.lua" {
		t.Fatalf("scripts active = %q", got)
	}
	if got := s.pipe.Stats()["script"]; got != "proto.lua" {
		t.Fatalf("parser script = %v", got)
	}
}

// 切换活动脚本：装载内容 + 写回 project.json。
func TestSetActiveScriptSwitchesAndPersists(t *testing.T) {
	s, _, _, dir := newEditorFixture(t, "function on_frame(raw)\n  return { main = 1 }\nend\n")
	editor := &EditorAPI{s: s}

	if err := editor.CreateScript("alt.lua"); err != nil {
		t.Fatal(err)
	}
	if _, err := editor.SaveScript("alt.lua", "function on_frame(raw)\n  return { alt = 1 }\nend\n"); err != nil {
		t.Fatal(err)
	}
	if err := editor.SetActiveScript("alt.lua"); err != nil {
		t.Fatal(err)
	}
	meta, err := project.LoadMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ActiveScript != "alt.lua" {
		t.Fatalf("active_script = %q", meta.ActiveScript)
	}
	if got := s.pipe.Stats()["script"]; got != "alt.lua" {
		t.Fatalf("parser script = %v", got)
	}
}

// ---------- 辅助 ----------

func newEditorFixture(t *testing.T, script string) (*Services, *mockPort, *eventLog, string) {
	t.Helper()
	s, port, log := newWiredServices(t)
	dir := t.TempDir()
	meta, err := project.Scaffold(dir, "editor", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", meta.ActiveScript), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Project.svc.Open(dir); err != nil {
		t.Fatal(err)
	}
	return s, port, log, dir
}

func connectMock(t *testing.T, s *Services) error {
	t.Helper()
	return s.Serial.Connect(map[string]any{
		"port": "MOCK", "baudrate": 115200, "data_bits": 8,
		"stop_bits": 1, "parity": "N", "flow_control": "none",
	})
}

// waitFields 等待任一帧满足条件（帧会持续累积，不能只看第一帧）。
func waitFields(t *testing.T, log *eventLog, ok func(map[string]any) bool, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		log.mu.Lock()
		for _, f := range log.frames {
			fields, _ := f["fields"].(map[string]any)
			if fields != nil && ok(fields) {
				log.mu.Unlock()
				return fields
			}
		}
		log.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}
