package project

import (
	"errors"
	"sync"
	"testing"
)

// fakeHooks 记录切换钩子调用顺序，并可注入失败。
type fakeHooks struct {
	mu          sync.Mutex
	calls       []string
	stateInHook State
	beforeErr   error
}

func (h *fakeHooks) BeforeSwitch() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, "before")
	return h.beforeErr
}

func (h *fakeHooks) AfterOpen(dir string, meta *ProjectMeta) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, "after:"+meta.Name)
}

func (h *fakeHooks) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.calls)
}

func (h *fakeHooks) countBefore() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.calls {
		if c == "before" {
			n++
		}
	}
	return n
}

func newSwitchTestService(t *testing.T) (*Service, *fakeHooks) {
	t.Helper()
	recents := NewRecentStore(t.TempDir())
	svc := NewService(recents, nil)
	hooks := &fakeHooks{}
	svc.SetHooks(hooks)
	return svc, hooks
}

func TestSwitchCallsHooksInOrder(t *testing.T) {
	svc, hooks := newSwitchTestService(t)
	dirA := t.TempDir()
	dirB := t.TempDir()
	if err := svc.Create(dirA, "A", ""); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if err := svc.Create(dirB, "B", ""); err != nil {
		t.Fatalf("create B: %v", err)
	}
	// Create(A) 只走 AfterOpen；切到 B 才走 BeforeSwitch。
	if got := hooks.calls; len(got) != 3 || got[0] != "after:A" || got[1] != "before" || got[2] != "after:B" {
		t.Fatalf("hooks = %v, want [after:A before after:B]", got)
	}
	if dir, meta, ok := svc.Current(); !ok || dir != dirB || meta.Name != "B" {
		t.Fatalf("current = %v %v %v", dir, meta, ok)
	}
	if svc.State() != StateProjectOpen {
		t.Fatalf("state = %v, want ProjectOpen", svc.State())
	}
}

// 打开同一工程不应触发拆卸（避免白白断开串口）。
func TestOpenSameDirSkipsHooks(t *testing.T) {
	svc, hooks := newSwitchTestService(t)
	dir := t.TempDir()
	if err := svc.Create(dir, "A", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Open(dir); err != nil {
		t.Fatal(err)
	}
	// 重开同一工程可以重载脚本（AfterOpen），但绝不能拆卸（BeforeSwitch）
	if n := hooks.countBefore(); n != 0 {
		t.Fatalf("BeforeSwitch called %d times for same-dir open: %v", n, hooks.calls)
	}
}

// 拆卸失败：保持原工程（禁止半加载），状态回到 ProjectOpen 并上报 project:error。
func TestSwitchFailureKeepsPreviousProject(t *testing.T) {
	svc, hooks := newSwitchTestService(t)
	var mu sync.Mutex
	var errs []map[string]any
	svc.SetEmitter(func(event string, payload map[string]any) {
		if event == "project:error" {
			mu.Lock()
			errs = append(errs, payload)
			mu.Unlock()
		}
	})
	dirA := t.TempDir()
	dirB := t.TempDir()
	if err := svc.Create(dirA, "A", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Scaffold(dirB, "B", ""); err != nil {
		t.Fatal(err)
	}

	hooks.beforeErr = errors.New("serial busy")
	if err := svc.Open(dirB); err == nil {
		t.Fatal("expected switch error")
	}
	if dir, meta, ok := svc.Current(); !ok || dir != dirA || meta.Name != "A" {
		t.Fatalf("current = %v %v %v, want A", dir, meta, ok)
	}
	if svc.State() != StateProjectOpen {
		t.Fatalf("state = %v, want ProjectOpen", svc.State())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(errs) != 1 || errs[0]["code"] != "project_switch_failed" {
		t.Fatalf("errors = %v", errs)
	}
}

// 新工程加载失败（预校验）：不应拆卸旧工程。
func TestOpenInvalidProjectDoesNotTeardown(t *testing.T) {
	svc, hooks := newSwitchTestService(t)
	dirA := t.TempDir()
	if err := svc.Create(dirA, "A", ""); err != nil {
		t.Fatal(err)
	}
	before := hooks.count()

	bad := t.TempDir()
	if err := writeFileRaw(bad+"/project.json", []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	if err := svc.Open(bad); err == nil {
		t.Fatal("expected load error")
	}
	if hooks.count() != before {
		t.Fatalf("hooks called despite validation failure: %v", hooks.calls)
	}
	if dir, _, ok := svc.Current(); !ok || dir != dirA {
		t.Fatalf("current = %v, want %v", dir, dirA)
	}
}

// 关闭工程也要走拆卸（断开串口 + 清字节码），最后落到 NoProject。
func TestCloseRunsHooksAndResetsState(t *testing.T) {
	svc, hooks := newSwitchTestService(t)
	dir := t.TempDir()
	if err := svc.Create(dir, "A", ""); err != nil {
		t.Fatal(err)
	}
	before := hooks.count()
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	if hooks.count() != before+1 {
		t.Fatalf("hooks = %v, want one before call", hooks.calls)
	}
	if svc.State() != StateNoProject {
		t.Fatalf("state = %v", svc.State())
	}
	if _, _, ok := svc.Current(); ok {
		t.Fatal("current should be empty after close")
	}
}
