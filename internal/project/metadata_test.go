package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const sampleJSON = `{
  "version": "1.0",
  "name": "MySensorProject",
  "description": "温湿度传感器调试",
  "created_at": "2026-11-13T14:30:22+08:00",
  "updated_at": "2026-11-13T16:08:15+08:00",
  "serial_config": {
    "baudrate": 115200, "data_bits": 8, "stop_bits": 1,
    "parity": "N", "flow_control": "none", "last_port": "/dev/ttyUSB0"
  },
  "active_script": "main.lua",
  "agent_settings": {
    "max_attempts": 3,
    "model_override": null,
    "context_hints": "帧头0xAA"
  }
}`

func writeProjectJSON(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "project.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMetaParsesSample(t *testing.T) {
	dir := t.TempDir()
	writeProjectJSON(t, dir, sampleJSON)
	m, err := LoadMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "MySensorProject" || m.Version != "1.0" {
		t.Errorf("name/version = %s/%s", m.Name, m.Version)
	}
	if m.SerialConfig.Baudrate != 115200 || m.SerialConfig.DataBits != 8 || m.SerialConfig.StopBits != 1 ||
		m.SerialConfig.Parity != "N" || m.SerialConfig.FlowControl != "none" || m.SerialConfig.LastPort != "/dev/ttyUSB0" {
		t.Errorf("serial_config = %+v", m.SerialConfig)
	}
	if m.ActiveScript != "main.lua" {
		t.Errorf("active_script = %s", m.ActiveScript)
	}
	if m.AgentSettings.MaxAttempts != 3 || m.AgentSettings.ContextHints != "帧头0xAA" {
		t.Errorf("agent_settings = %+v", m.AgentSettings)
	}
}

func TestLoadMetaDirMissing(t *testing.T) {
	_, err := LoadMeta(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("want error for missing dir")
	}
}

func TestLoadMetaCorrupt(t *testing.T) {
	cases := []struct{ name, json string }{
		{"invalid json", `{"name": "x",`},
		{"missing name", `{"version":"1.0","serial_config":{"baudrate":115200}}`},
		{"bad baudrate", `{"version":"1.0","name":"x","serial_config":{"baudrate":100}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "p")
			writeProjectJSON(t, dir, c.json)
			if _, err := LoadMeta(dir); err == nil {
				t.Errorf("%s: want error, got nil", c.name)
			}
		})
	}
}

func TestSaveMetaRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	m := DefaultMeta("") // 空 name 非法
	if err := SaveMeta(dir, m); err == nil {
		t.Fatal("want error for empty name")
	}
	if _, err := os.Stat(filepath.Join(dir, "project.json")); !os.IsNotExist(err) {
		t.Fatal("invalid meta must not be written")
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "测试 工程 #1")
	m := DefaultMeta("Sensor")
	m.SerialConfig.LastPort = "COM3"
	if err := SaveMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	got, err := LoadMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Sensor" || got.SerialConfig.Baudrate != 115200 || got.SerialConfig.LastPort != "COM3" {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	if !got.CreatedAt.Equal(m.CreatedAt) {
		t.Errorf("created_at changed: %v -> %v", m.CreatedAt, got.CreatedAt)
	}
}

func TestIsProject(t *testing.T) {
	dir := t.TempDir()
	if IsProject(dir) {
		t.Error("empty dir must not be a project")
	}
	writeProjectJSON(t, dir, sampleJSON)
	if !IsProject(dir) {
		t.Error("dir with valid project.json must be a project")
	}
}

func TestUpdatedAtRefreshedOnSave(t *testing.T) {
	dir := t.TempDir()
	m := DefaultMeta("X")
	if err := SaveMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "project.json"))
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["updated_at"]; !ok {
		t.Error("updated_at must be present in JSON (snake_case)")
	}
	if _, ok := doc["serial_config"]; !ok {
		t.Error("serial_config key must be snake_case")
	}
}
