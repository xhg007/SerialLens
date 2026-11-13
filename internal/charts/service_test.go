package charts

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"seriallens/internal/events"
)

type recorder struct {
	mu      sync.Mutex
	updated []string // source
	configs []map[string]any
	errors  []string
}

func (r *recorder) emit(name string, payload map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch name {
	case events.EvChartsUpdated:
		r.updated = append(r.updated, payload["source"].(string))
		cfg, _ := payload["config"].(map[string]any)
		r.configs = append(r.configs, cfg)
	case events.EvChartsError:
		r.errors = append(r.errors, payload["message"].(string))
	}
}

func (r *recorder) sources() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.updated...)
}

func (r *recorder) lastConfig() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.configs) == 0 {
		return nil
	}
	return r.configs[len(r.configs)-1]
}

func (r *recorder) lastError() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.errors) == 0 {
		return ""
	}
	return r.errors[len(r.errors)-1]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestLoadCreatesDefaultWhenMissing(t *testing.T) {
	dir := t.TempDir()
	rec := &recorder{}
	s := NewService(rec.emit)
	defer s.Close()

	if err := s.Load(dir); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Fatalf("charts.json should be created: %v", err)
	}
	if got := rec.sources(); len(got) != 1 || got[0] != "project" {
		t.Fatalf("sources = %v, want [project]", got)
	}
	cfg := rec.lastConfig()
	if cfg == nil || len(cfg["widgets"].([]any)) == 0 {
		t.Fatalf("config payload incomplete: %#v", cfg)
	}
}

func TestExternalEditTriggersReload(t *testing.T) {
	dir := t.TempDir()
	rec := &recorder{}
	s := NewService(rec.emit)
	defer s.Close()
	if err := s.Load(dir); err != nil {
		t.Fatal(err)
	}

	// 模拟用户用编辑器改配置：只留一个 indicator
	edited := &Config{
		Version: "1.0",
		Layout:  Layout{Grid: Grid{Cols: 12, RowHeight: 100}},
		Widgets: []Widget{{
			ID: "led", Type: TypeIndicator, Title: "在线",
			Position: Position{X: 0, Y: 0, W: 2, H: 1},
			Channels: ChannelList{Items: []Channel{{Name: "device_ok"}}},
		}},
	}
	if err := Save(dir, edited); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "external reload", func() bool {
		src := rec.sources()
		return len(src) >= 2 && src[len(src)-1] == "external"
	})

	cfg := s.Config()
	if cfg == nil || len(cfg.Widgets) != 1 || cfg.Widgets[0].ID != "led" {
		t.Fatalf("config not reloaded: %+v", cfg)
	}
	if cfg.Layout.Grid.RowHeight != 100 {
		t.Fatalf("row_height = %d, want 100", cfg.Layout.Grid.RowHeight)
	}
}

func TestSelfWriteDoesNotLoop(t *testing.T) {
	dir := t.TempDir()
	rec := &recorder{}
	s := NewService(rec.emit)
	defer s.Close()
	if err := s.Load(dir); err != nil {
		t.Fatal(err)
	}

	cfg := s.Config()
	cfg.Widgets[1].Title = "改过的标题"
	if err := s.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ui save", func() bool {
		src := rec.sources()
		return len(src) >= 2 && src[len(src)-1] == "ui"
	})
	// 自身写入不应再触发一次 external 重载
	time.Sleep(reloadDebounce + 300*time.Millisecond)
	if src := rec.sources(); len(src) != 2 {
		t.Fatalf("sources = %v, want exactly [project ui]（自写不得回环）", src)
	}
}

func TestInvalidExternalEditKeepsOldConfig(t *testing.T) {
	dir := t.TempDir()
	rec := &recorder{}
	s := NewService(rec.emit)
	defer s.Close()
	if err := s.Load(dir); err != nil {
		t.Fatal(err)
	}
	before := len(s.Config().Widgets)

	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"widgets":[{"id":"x","type":"pie","position":{"w":1,"h":1}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "charts:error", func() bool { return rec.lastError() != "" })
	if got := len(s.Config().Widgets); got != before {
		t.Fatalf("widgets = %d, want unchanged %d", got, before)
	}
	// 非法内容不得被我们改写覆盖（用户要能自己修）
	raw, _ := os.ReadFile(filepath.Join(dir, FileName))
	if !strings.Contains(string(raw), "pie") {
		t.Fatal("invalid file must be left untouched for the user to fix")
	}
}

func TestCloseStopsWatching(t *testing.T) {
	dir := t.TempDir()
	rec := &recorder{}
	s := NewService(rec.emit)
	if err := s.Load(dir); err != nil {
		t.Fatal(err)
	}
	s.Close()

	cfg := Default()
	cfg.Widgets[0].Title = "after close"
	if err := Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	time.Sleep(reloadDebounce + 300*time.Millisecond)
	if src := rec.sources(); len(src) != 1 {
		t.Fatalf("watch kept running after Close: %v", src)
	}
}

func TestSaveConfigWithoutProject(t *testing.T) {
	s := NewService(nil)
	if err := s.SaveConfig(Default()); err != ErrNoProject {
		t.Fatalf("err = %v, want ErrNoProject", err)
	}
}

func TestSwitchProjectRebindsWatcher(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	rec := &recorder{}
	s := NewService(rec.emit)
	defer s.Close()

	if err := s.Load(dirA); err != nil {
		t.Fatal(err)
	}
	if err := s.Load(dirB); err != nil {
		t.Fatal(err)
	}
	// 改 B 应生效
	cfgB := Default()
	cfgB.Widgets[0].Title = "B 的标题"
	if err := Save(dirB, cfgB); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "B reload", func() bool { return s.Config().Widgets[0].Title == "B 的标题" })

	// 改 A 不应再生效（旧 watcher 必须已关闭）
	cfgA := Default()
	cfgA.Widgets[0].Title = "A 的标题"
	if err := Save(dirA, cfgA); err != nil {
		t.Fatal(err)
	}
	time.Sleep(reloadDebounce + 300*time.Millisecond)
	if got := s.Config().Widgets[0].Title; got == "A 的标题" {
		t.Fatal("watcher still bound to previous project dir")
	}
}
