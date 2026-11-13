package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	chartsRaw, err := os.ReadFile(filepath.Join(dir, "charts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var charts map[string]any
	if err := json.Unmarshal(chartsRaw, &charts); err != nil {
		t.Errorf("charts.json invalid: %v", err)
	}
	if charts["version"] != "1.0" {
		t.Errorf("charts.json version = %v", charts["version"])
	}
}
