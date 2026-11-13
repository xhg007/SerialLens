package scripts

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"seriallens/internal/events"
	"seriallens/internal/lua"
)

type recorder struct {
	mu     sync.Mutex
	events []string
	loads  []string // 装载过的脚本内容
	last   map[string]any
}

func (r *recorder) emit(name string, payload map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, name)
	r.last = payload
}

func (r *recorder) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e == name {
			n++
		}
	}
	return n
}

func (r *recorder) loadsCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.loads)
}

// newSvc 建一个已打开工程目录的服务，loader 只做编译校验。
func newSvc(t *testing.T, dir, active string) (*Service, *recorder) {
	t.Helper()
	rec := &recorder{}
	s := New(rec.emit)
	s.SetLoader(func(name string, src []byte) error {
		if err := lua.Compile(name, src); err != nil {
			return err
		}
		rec.mu.Lock()
		rec.loads = append(rec.loads, string(src))
		rec.mu.Unlock()
		return nil
	})
	if err := s.Open(dir, active); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, rec
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListReadAndOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.lua", "function on_frame() end\n")
	writeFile(t, dir, "b.lua", "function on_frame() end\n")
	writeFile(t, dir, "ignore.txt", "nope")

	s, _ := newSvc(t, dir, "main.lua")
	files, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2 (txt must be ignored)", len(files))
	}
	if files[0].Name != "main.lua" || !files[0].Active {
		t.Fatalf("active script must be first: %+v", files[0])
	}
	if _, err := s.Read("ignore.txt"); err != ErrBadName {
		t.Fatalf("read non-lua = %v, want ErrBadName", err)
	}
}

func TestNameValidation(t *testing.T) {
	bad := []string{"", "..", "a/b.lua", `a\b.lua`, ".hidden.lua", "x.txt", "C:evil.lua", "a.lua.bak"}
	for _, n := range bad {
		if err := ValidateName(n); err == nil {
			t.Errorf("ValidateName(%q) should fail", n)
		}
	}
	for _, n := range []string{"main.lua", "my-script_2.lua", "UPPER.LUA"} {
		if err := ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) = %v", n, err)
		}
	}
}

func TestCRUD(t *testing.T) {
	dir := t.TempDir()
	s, _ := newSvc(t, dir, "main.lua")

	if err := s.Create("main.lua"); err != nil { // Open 不会自动建 main.lua
		t.Fatalf("create main: %v", err)
	}
	if err := s.Create("main.lua"); err != ErrExists {
		t.Fatalf("duplicate create = %v, want ErrExists", err)
	}
	if err := s.Duplicate("main.lua", "copy.lua"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Read("copy.lua"); !strings.Contains(got, "on_frame") {
		t.Fatalf("copy content = %q", got)
	}
	if err := s.Rename("copy.lua", "renamed.lua"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read("copy.lua"); err != ErrNotFound {
		t.Fatalf("old name still readable: %v", err)
	}
	if err := s.Remove("renamed.lua"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("main.lua"); err != ErrActiveScript {
		t.Fatalf("remove active = %v, want ErrActiveScript", err)
	}
}

// 保存活动脚本 = 写盘 + 立即装载（PRD §2.2 用例 3）。
func TestSaveActiveScriptLoadsImmediately(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.lua", "function on_frame() end\n")
	s, rec := newSvc(t, dir, "main.lua")

	src := "function on_frame(raw)\n  return { v = 1 }\nend\n"
	res, err := s.Save("main.lua", src)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Diagnostic != nil {
		t.Fatalf("save result = %+v", res)
	}
	if rec.loadsCount() != 1 {
		t.Fatalf("loader called %d times, want 1", rec.loadsCount())
	}
	if rec.count(events.EvScriptReloaded) != 1 {
		t.Fatalf("no script:reloaded event: %v", rec.events)
	}
	if onDisk, _ := os.ReadFile(filepath.Join(dir, "scripts", "main.lua")); string(onDisk) != src {
		t.Fatal("file not written")
	}
}

// 语法错误：内容必须落盘（不能吞用户编辑），但旧脚本继续跑，并带回行号。
func TestSaveWithSyntaxErrorKeepsOldScript(t *testing.T) {
	dir := t.TempDir()
	src := "function on_frame(raw)\n  return { v = 1 }\nend\n"
	writeFile(t, dir, "main.lua", src)
	s, rec := newSvc(t, dir, "main.lua")

	bad := "function on_frame(raw)\n  local x = +\n  return x\nend\n"
	res, err := s.Save("main.lua", bad)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Diagnostic == nil {
		t.Fatalf("expected diagnostic, got %+v", res)
	}
	if res.Diagnostic.Line != 2 {
		t.Fatalf("diagnostic line = %d, want 2 (%+v)", res.Diagnostic.Line, res.Diagnostic)
	}
	if rec.loadsCount() != 0 {
		t.Fatalf("broken script must not be loaded, loads=%d", rec.loadsCount())
	}
	if rec.count(events.EvScriptError) != 1 {
		t.Fatalf("no script:error event: %v", rec.events)
	}
	if onDisk, _ := os.ReadFile(filepath.Join(dir, "scripts", "main.lua")); string(onDisk) != bad {
		t.Fatal("user edit must be persisted even when it does not compile")
	}
	files, _ := s.List()
	if !files[0].HasError {
		t.Fatal("list should mark the broken script")
	}
}

// 非活动脚本只编译校验，不装载。
func TestSaveInactiveScriptDoesNotLoad(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.lua", "function on_frame() end\n")
	s, rec := newSvc(t, dir, "main.lua")

	res, err := s.Save("other.lua", "function on_frame()\n return {}\nend\n")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || rec.loadsCount() != 0 {
		t.Fatalf("inactive save loaded: %+v loads=%d", res, rec.loadsCount())
	}
	if rec.count(events.EvScriptReloaded) != 0 {
		t.Fatal("inactive save must not emit script:reloaded")
	}
}

// 外部 IDE 修改：100ms 防抖后装载，并把内容推给编辑器。
func TestExternalEditReloads(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.lua", "function on_frame() end\n")
	_, rec := newSvc(t, dir, "main.lua")

	updated := "function on_frame(raw)\n  return { n = #raw }\nend\n"
	if err := os.WriteFile(filepath.Join(dir, "scripts", "main.lua"), []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if rec.loadsCount() > 0 && rec.count(events.EvScriptChanged) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rec.loadsCount() == 0 {
		t.Fatal("external edit did not reload the script")
	}
	if rec.count(events.EvScriptChanged) == 0 {
		t.Fatal("editor not notified about external edit")
	}
	rec.mu.Lock()
	last := rec.last
	rec.mu.Unlock()
	_ = last
}

// 自己写盘不能再触发一次外部装载（否则每次保存都装两遍）。
func TestSelfWriteDoesNotDoubleLoad(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.lua", "function on_frame() end\n")
	s, rec := newSvc(t, dir, "main.lua")

	if _, err := s.Save("main.lua", "function on_frame(raw)\n return { a = 1 }\nend\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond) // 穿过防抖窗口
	if n := rec.loadsCount(); n != 1 {
		t.Fatalf("loader called %d times, want 1 (self-write must be skipped)", n)
	}
}

func TestRenameActiveKeepsActive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.lua", "function on_frame() end\n")
	s, _ := newSvc(t, dir, "main.lua")

	if err := s.Rename("main.lua", "proto.lua"); err != nil {
		t.Fatal(err)
	}
	if got := s.Active(); got != "proto.lua" {
		t.Fatalf("active = %q, want proto.lua", got)
	}
}

func TestSaveToUnknownProjectFails(t *testing.T) {
	s := New(nil)
	if _, err := s.Save("main.lua", "x"); err != ErrNoProject {
		t.Fatalf("save without project = %v", err)
	}
	if _, err := s.List(); err != ErrNoProject {
		t.Fatalf("list without project = %v", err)
	}
}

func TestSetActiveLoadsAndEmitsList(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.lua", "function on_frame() end\n")
	writeFile(t, dir, "alt.lua", "function on_frame(raw)\n return { alt = 1 }\nend\n")
	s, rec := newSvc(t, dir, "main.lua")

	if err := s.SetActive("alt.lua"); err != nil {
		t.Fatal(err)
	}
	if got := s.Active(); got != "alt.lua" {
		t.Fatalf("active = %q", got)
	}
	if rec.loadsCount() != 1 || rec.count(events.EvScriptList) == 0 {
		t.Fatalf("loads=%d events=%v", rec.loadsCount(), rec.events)
	}
}
