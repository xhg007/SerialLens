package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"seriallens/internal/events"
)

// I11: emit 不得在持有 s.mu 时调用（否则监听者回调 Current() 自死锁）。
func TestServiceEmitOutsideLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "P")
	if _, err := Scaffold(dir, "P", ""); err != nil {
		t.Fatal(err)
	}

	var svc *Service
	done := make(chan struct{})
	var once sync.Once
	emit := func(event string, payload map[string]any) {
		if event == events.EvProjectOpened {
			once.Do(func() {
				// 监听者回调里读取状态：持锁 emit 会死锁
				_ = svc.State()
				_, _, _ = svc.Current()
				close(done)
			})
		}
	}
	svc = NewService(NewRecentStore(t.TempDir()), emit)
	go func() { _ = svc.Open(dir) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emit called under lock: State() deadlocked")
	}
}

// I6/I7: Validate 必须覆盖 serial_config 全部字段与 active_script 路径安全。
func TestValidateStrictBounds(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(m *ProjectMeta)
	}{
		{"bad data_bits", func(m *ProjectMeta) { m.SerialConfig.DataBits = 42 }},
		{"bad stop_bits", func(m *ProjectMeta) { m.SerialConfig.StopBits = 3 }},
		{"bad flow_control", func(m *ProjectMeta) { m.SerialConfig.FlowControl = "bogus" }},
		{"path escape script", func(m *ProjectMeta) { m.ActiveScript = "../evil.lua" }},
		{"abs script", func(m *ProjectMeta) { m.ActiveScript = "C:\\evil.lua" }},
		{"non lua", func(m *ProjectMeta) { m.ActiveScript = "main.txt" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := DefaultMeta("X")
			c.mutate(m)
			if err := Validate(m); err == nil {
				t.Errorf("%s: want error, got nil", c.name)
			}
		})
	}
}

// I9: recent_projects.json 必须原子写（无 .tmp 残留、损坏文件不静默清空）。
func TestRecentSaveAtomic(t *testing.T) {
	home := t.TempDir()
	r := NewRecentStore(home)
	if err := r.RecordOpen("/a", "A"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "recent_projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("file not valid json: %v", err)
	}
	entries, _ := os.ReadDir(home)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("tmp file left behind: %s", e.Name())
		}
	}
}

// I9: 损坏文件被记录为 .bak，且下一次写入不静默丢弃原内容。
func TestRecentCorruptFileBackedUp(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "recent_projects.json")
	if err := writeFileRaw(path, []byte("{corrupt")); err != nil {
		t.Fatal(err)
	}
	r := NewRecentStore(home)
	_ = r.List()
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("corrupt file must be backed up as .bak: %v", err)
	}
}

// C2: Scaffold 失败时不得删除用户已有目录。
func TestScaffoldFailureKeepsExistingDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "scripts"), []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Scaffold(dir, "X", ""); err == nil {
		t.Fatal("scaffold should fail when scripts/ is a file")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Error("pre-existing dir must not be deleted")
	}
}
