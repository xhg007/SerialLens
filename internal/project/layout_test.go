package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"seriallens/internal/charts"
)

func TestScaffoldCreatesLayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "MySensor")
	m, err := Scaffold(dir, "MySensor", "温湿度调试")
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "MySensor" || m.ActiveScript != "main.lua" {
		t.Errorf("meta = %+v", m)
	}
	want := []string{
		"project.json",
		"charts.json",
		"scripts/main.lua",
		"conversations",
		"data",
		filepath.Join(".seriallens", "samples"),
		filepath.Join(".seriallens", "drafts"),
	}
	for _, rel := range want {
		p := filepath.Join(dir, rel)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing after scaffold: %s", rel)
		}
	}
}

func TestScaffoldMetaReloadable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "P")
	if _, err := Scaffold(dir, "P", ""); err != nil {
		t.Fatal(err)
	}
	m, err := LoadMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.ActiveScript != "main.lua" {
		t.Errorf("active_script = %s", m.ActiveScript)
	}
}

func TestScaffoldRefusesExistingProject(t *testing.T) {
	dir := t.TempDir()
	writeProjectJSON(t, dir, sampleJSON)
	if _, err := Scaffold(dir, "X", ""); err == nil {
		t.Fatal("scaffold must refuse a directory that already has project.json")
	}
}

func TestScaffoldTemplateContents(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "T")
	if _, err := Scaffold(dir, "T", ""); err != nil {
		t.Fatal(err)
	}
	lua, err := os.ReadFile(filepath.Join(dir, "scripts", "main.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lua), "on_frame") {
		t.Error("main.lua must contain on_frame contract")
	}
	// 模板里的 charts.json 必须能通过 charts 包的 schema 校验，且开箱就有可用 widget
	// （否则新工程打开后图表区一片空白，用户以为功能坏了）。
	cfg, existed, err := charts.Load(dir)
	if err != nil || !existed {
		t.Fatalf("template charts.json invalid: %v (existed=%v)", err, existed)
	}
	if len(cfg.Widgets) == 0 {
		t.Fatal("blank template must ship usable widgets")
	}
	if cfg.Widgets[0].Type != charts.TypeLine {
		t.Fatalf("first widget type = %s, want line", cfg.Widgets[0].Type)
	}
}
