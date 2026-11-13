package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"seriallens/internal/charts"
	"seriallens/internal/project"
)

// 打开工程必须同时加载 charts.json；缺文件时自动生成默认配置（首启可用）。
func TestProjectOpenLoadsChartsConfig(t *testing.T) {
	s, _, _ := newWiredServices(t)

	dir := t.TempDir()
	if _, err := project.Scaffold(dir, "charts-a", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Project.svc.Open(dir); err != nil {
		t.Fatal(err)
	}

	cfg := s.charts.Config()
	if cfg == nil {
		t.Fatal("charts config not loaded on project open")
	}
	if len(cfg.Widgets) == 0 {
		t.Fatal("default config should have widgets")
	}
	if _, err := os.Stat(filepath.Join(dir, charts.FileName)); err != nil {
		t.Fatalf("charts.json should be generated: %v", err)
	}
}

// 切换工程必须换掉图表配置与监听目标（旧工程的 charts.json 改动不能再影响 UI）。
func TestProjectSwitchReloadsCharts(t *testing.T) {
	s, _, _ := newWiredServices(t)

	dirA, dirB := t.TempDir(), t.TempDir()
	for _, d := range []string{dirA, dirB} {
		if _, err := project.Scaffold(d, filepath.Base(d), ""); err != nil {
			t.Fatal(err)
		}
	}
	// A 用自定义单 widget 配置
	custom := &charts.Config{
		Version: "1.0",
		Layout:  charts.Layout{Grid: charts.Grid{Cols: 12, RowHeight: 90}},
		Widgets: []charts.Widget{{
			ID: "led", Type: charts.TypeIndicator, Title: "在线",
			Position: charts.Position{X: 0, Y: 0, W: 2, H: 1},
			Channels: charts.ChannelList{Items: []charts.Channel{{Name: "device_ok"}}},
		}},
	}
	if err := charts.Save(dirA, custom); err != nil {
		t.Fatal(err)
	}

	if err := s.Project.svc.Open(dirA); err != nil {
		t.Fatal(err)
	}
	if got := s.charts.Config(); got == nil || len(got.Widgets) != 1 || got.Widgets[0].ID != "led" {
		t.Fatalf("A config not loaded: %+v", got)
	}

	if err := s.Project.svc.Open(dirB); err != nil {
		t.Fatal(err)
	}
	got := s.charts.Config()
	if got == nil {
		t.Fatal("no charts config after switch")
	}
	if len(got.Widgets) == 1 && got.Widgets[0].ID == "led" {
		t.Fatal("charts config still from previous project")
	}

	// 改 A 的 charts.json 不应再影响当前 UI
	custom.Widgets[0].Title = "A 改过了"
	if err := charts.Save(dirA, custom); err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	if cur := s.charts.Config(); cur != nil && len(cur.Widgets) == 1 && cur.Widgets[0].Title == "A 改过了" {
		t.Fatal("charts watcher still bound to previous project")
	}
}

// 关闭工程必须停止图表监听并清空配置。
func TestProjectCloseStopsChartsWatch(t *testing.T) {
	s, _, _ := newWiredServices(t)

	dir := t.TempDir()
	if _, err := project.Scaffold(dir, "charts-close", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Project.svc.Open(dir); err != nil {
		t.Fatal(err)
	}
	if err := s.Project.svc.Close(); err != nil {
		t.Fatal(err)
	}
	if got := s.charts.Config(); got != nil {
		t.Fatalf("charts config not cleared on close: %+v", got)
	}
}
