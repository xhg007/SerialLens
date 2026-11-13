package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SerialConfig 串口连接参数，打开串口时自动填入（PRD §3.3）。
type SerialConfig struct {
	Baudrate    int    `json:"baudrate"`
	DataBits    int    `json:"data_bits"`
	StopBits    int    `json:"stop_bits"`
	Parity      string `json:"parity"`         // N / E / O
	FlowControl string `json:"flow_control"`   // none / rtscts / xonxoff
	LastPort    string `json:"last_port"`
}

// AgentSettings AI Agent 工程级配置（PRD §3.3）。
type AgentSettings struct {
	MaxAttempts   int    `json:"max_attempts"`
	ModelOverride string `json:"model_override"`
	ContextHints  string `json:"context_hints"`
}

// ProjectMeta 工程元数据，project.json 的 Go 映射。
// project.json 存在且合法即视为合法工程（PRD §3.2）。
type ProjectMeta struct {
	Version       string        `json:"version"`
	Name          string        `json:"name"`
	Description   string        `json:"description"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
	SerialConfig  SerialConfig  `json:"serial_config"`
	ActiveScript  string        `json:"active_script"`
	AgentSettings AgentSettings `json:"agent_settings"`
}

// DefaultMeta 返回带默认值的工程元数据（PRD §3.3 默认串口参数）。
func DefaultMeta(name string) *ProjectMeta {
	now := time.Now()
	return &ProjectMeta{
		Version:  "1.0",
		Name:     name,
		CreatedAt: now,
		UpdatedAt: now,
		SerialConfig: SerialConfig{
			Baudrate:    115200,
			DataBits:    8,
			StopBits:    1,
			Parity:      "N",
			FlowControl: "none",
		},
		ActiveScript: "main.lua",
		AgentSettings: AgentSettings{
			MaxAttempts: 3,
		},
	}
}

// Validate 校验元数据合法性；失败不落盘（PRD §7.5）。
func Validate(m *ProjectMeta) error {
	if m == nil {
		return errors.New("meta is nil")
	}
	if m.Name == "" {
		return errors.New("name is required")
	}
	if m.Version != "1.0" {
		return fmt.Errorf("unsupported version %q", m.Version)
	}
	if m.SerialConfig.Baudrate < 1200 || m.SerialConfig.Baudrate > 10000000 {
		return fmt.Errorf("baudrate %d out of range [1200, 10000000]", m.SerialConfig.Baudrate)
	}
	switch m.SerialConfig.Parity {
	case "N", "E", "O":
	default:
		return fmt.Errorf("invalid parity %q", m.SerialConfig.Parity)
	}
	return nil
}

const metaFileName = "project.json"

// IsProject 判断目录是否为合法工程。
func IsProject(dir string) bool {
	_, err := LoadMeta(dir)
	return err == nil
}

// LoadMeta 读取并校验 <dir>/project.json。
func LoadMeta(dir string) (*ProjectMeta, error) {
	raw, err := os.ReadFile(filepath.Join(dir, metaFileName))
	if err != nil {
		return nil, fmt.Errorf("read project.json: %w", err)
	}
	var m ProjectMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse project.json: %w", err)
	}
	if err := Validate(&m); err != nil {
		return nil, fmt.Errorf("invalid project.json: %w", err)
	}
	return &m, nil
}

// SaveMeta 校验后原子写入 <dir>/project.json，并刷新 UpdatedAt。
func SaveMeta(dir string, m *ProjectMeta) error {
	if err := Validate(m); err != nil {
		return err
	}
	m.UpdatedAt = time.Now()
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, metaFileName+".tmp")
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, metaFileName))
}
