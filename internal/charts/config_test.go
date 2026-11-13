package charts

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingReturnsDefault(t *testing.T) {
	dir := t.TempDir()
	cfg, existed, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if existed {
		t.Fatal("existed should be false for missing file")
	}
	if len(cfg.Widgets) == 0 {
		t.Fatal("default config must have widgets")
	}
	if _, statErr := os.Stat(filepath.Join(dir, FileName)); !os.IsNotExist(statErr) {
		t.Fatal("Load must not write the file")
	}
}

func TestSaveThenLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := Default()
	if err := Save(dir, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, existed, err := Load(dir)
	if err != nil || !existed {
		t.Fatalf("load: %v existed=%v", err, existed)
	}
	if len(got.Widgets) != len(cfg.Widgets) {
		t.Fatalf("widgets = %d, want %d", len(got.Widgets), len(cfg.Widgets))
	}
	// "auto" 必须原样保留（不能退化成空数组）
	raw, _ := os.ReadFile(filepath.Join(dir, FileName))
	if !strings.Contains(string(raw), `"channels": "auto"`) {
		t.Fatalf("auto channels lost in round trip:\n%s", raw)
	}
}

func TestSaveIsAtomicAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, Default()); err != nil {
		t.Fatal(err)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(matches) != 0 {
		t.Fatalf("temp files left: %v", matches)
	}
}

func TestInvalidConfigRejectedAndNotWritten(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, Default()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, FileName))

	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"unknown type", func(c *Config) { c.Widgets[0].Type = "pie" }, "unknown type"},
		{"empty id", func(c *Config) { c.Widgets[0].ID = " " }, "id is required"},
		{"dup id", func(c *Config) { c.Widgets[1].ID = c.Widgets[0].ID }, "duplicate widget id"},
		{"zero size", func(c *Config) { c.Widgets[0].Position.W = 0 }, "position w/h"},
		{"overflow cols", func(c *Config) {
			c.Widgets[0].Position.X = 10
			c.Widgets[0].Position.W = 6
		}, "exceeds grid columns"},
		{"bad range", func(c *Config) {
			c.Widgets[0].Channels = ChannelList{Items: []Channel{{Name: "a", Range: []float64{10, 1}}}}
		}, "range lo>=hi"},
		{"bad digits", func(c *Config) {
			d := 9
			c.Widgets[0].Channels = ChannelList{Items: []Channel{{Name: "a", Digits: &d}}}
		}, "display_digits"},
		{"dup channel", func(c *Config) {
			c.Widgets[0].Channels = ChannelList{Items: []Channel{{Name: "a"}, {Name: "a"}}}
		}, "duplicates channel"},
		{"bad window", func(c *Config) { c.Widgets[0].WindowSeconds = -1 }, "window_seconds"},
		{"bad axis", func(c *Config) { c.Widgets[0].YAxisMode = "log" }, "y_axis_mode"},
		{"too many widgets", func(c *Config) {
			for i := 0; i < maxWidgets+1; i++ {
				c.Widgets = append(c.Widgets, Widget{ID: "x" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
					Type: TypeIndicator, Position: Position{W: 1, H: 1}})
			}
		}, "too many widgets"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mutate(cfg)
			err := Save(dir, cfg)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("err = %v, want ErrInvalidConfig", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want contains %q", err, tc.want)
			}
			after, _ := os.ReadFile(filepath.Join(dir, FileName))
			if string(after) != string(before) {
				t.Fatal("invalid config must not overwrite charts.json")
			}
		})
	}
}

// Windows 记事本 / PowerShell 5.1 的 UTF-8 会带 BOM，手改 charts.json 时必须能读。
func TestLoadToleratesUTF8BOM(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"version":"1.0","layout":{"grid":{"cols":12,"row_height":80}},"widgets":[{"id":"w","type":"line","title":"t","position":{"x":0,"y":0,"w":6,"h":2},"channels":"auto"}]}`)
	raw := append([]byte{0xEF, 0xBB, 0xBF}, body...)
	if err := os.WriteFile(filepath.Join(dir, FileName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, existed, err := Load(dir)
	if err != nil || !existed {
		t.Fatalf("load with BOM failed: %v", err)
	}
	if len(cfg.Widgets) != 1 {
		t.Fatalf("widgets = %d, want 1", len(cfg.Widgets))
	}
}

func TestLoadRejectsBrokenJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(dir); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestChannelListParsing(t *testing.T) {
	cases := []struct {
		raw      string
		wantAuto bool
		wantLen  int
		wantErr  bool
	}{
		{`"auto"`, true, 0, false},
		{`[]`, true, 0, false},
		{`null`, true, 0, false},
		{`[{"name":"t"}]`, false, 1, false},
		{`"all"`, false, 0, true},
	}
	for _, tc := range cases {
		var cl ChannelList
		err := json.Unmarshal([]byte(tc.raw), &cl)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%s: want error", tc.raw)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		if cl.IsAuto() != tc.wantAuto || len(cl.Items) != tc.wantLen {
			t.Fatalf("%s: auto=%v items=%d", tc.raw, cl.IsAuto(), len(cl.Items))
		}
	}
}

func TestNormalizeFillsDefaults(t *testing.T) {
	cfg := &Config{Widgets: []Widget{{
		ID: "w", Type: TypeLine, Position: Position{W: 3, H: 2},
	}}}
	if err := cfg.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Version != "1.0" || cfg.Layout.Grid.Cols != 12 || cfg.Layout.Grid.RowHeight != 80 {
		t.Fatalf("defaults not applied: %+v", cfg.Layout)
	}
	w := cfg.Widgets[0]
	if w.Title != "w" || w.WindowSeconds != 60 || w.YAxisMode != AxisAuto {
		t.Fatalf("widget defaults not applied: %+v", w)
	}
}
