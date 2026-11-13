// Package charts 实现 charts.json 图表映射配置：加载、校验、落盘与热同步。
//
// charts.json 是脚本与界面的解耦层（PRD §3.5）：脚本只管 emit_chart(通道, 值)，
// 通道怎么渲染（折线/仪表/指示灯/字段表、量程、颜色、窗口）全部由本文件决定。
package charts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileName 工程内图表配置文件名。
const FileName = "charts.json"

// Widget 类型（PRD §3.5）。
const (
	TypeLine       = "line"
	TypeGauge      = "gauge"
	TypeIndicator  = "indicator"
	TypeFieldTable = "field_table"
)

// Y 轴模式。
const (
	AxisAuto  = "auto"
	AxisFixed = "fixed"
)

// 布局硬上限：防止一份配置把浏览器拖死。
const (
	maxCols      = 24
	maxWidgets   = 64
	maxRowHeight = 400
	minRowHeight = 20
)

// ErrInvalidConfig 配置未通过校验；调用方不得落盘。
var ErrInvalidConfig = errors.New("charts: invalid charts.json")

// ErrNoProject 当前没有打开的工程。
var ErrNoProject = errors.New("charts: no project open")

// Config 对应 charts.json 顶层结构。
type Config struct {
	Version string   `json:"version"`
	Layout  Layout   `json:"layout"`
	Widgets []Widget `json:"widgets"`
}

// Layout 栅格布局参数。
type Layout struct {
	Grid Grid `json:"grid"`
}

// Grid 12 列栅格，row_height 为单行像素高。
type Grid struct {
	Cols      int `json:"cols"`
	RowHeight int `json:"row_height"`
}

// Position 栅格坐标（单位=列/行，不是像素）。
type Position struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// Widget 一个图表面板。
type Widget struct {
	ID            string      `json:"id"`
	Type          string      `json:"type"`
	Title         string      `json:"title"`
	Position      Position    `json:"position"`
	Channels      ChannelList `json:"channels"`
	WindowSeconds int         `json:"window_seconds,omitempty"`
	YAxisMode     string      `json:"y_axis_mode,omitempty"`
}

// Channel 通道绑定与显示参数。
type Channel struct {
	Name      string    `json:"name"`
	Color     string    `json:"color,omitempty"`
	Unit      string    `json:"unit,omitempty"`
	Range     []float64 `json:"range,omitempty"`
	Digits    *int      `json:"display_digits,omitempty"`
	AlarmHigh *float64  `json:"alarm_high,omitempty"`
	AlarmLow  *float64  `json:"alarm_low,omitempty"`
	OnValue   *float64  `json:"on_value,omitempty"`
}

// ChannelList 支持两种写法：显式数组，或字符串 "auto"（绑定脚本推出的全部通道）。
//
//	"channels": [{"name":"temperature","color":"#FF5722"}]
//	"channels": "auto"
//
// 缺省/空数组同样视为 auto —— 新建工程时用户往往还没写 charts.json。
type ChannelList struct {
	Auto  bool
	Items []Channel
}

// IsAuto 报告是否自动绑定。
func (c ChannelList) IsAuto() bool { return c.Auto || len(c.Items) == 0 }

// UnmarshalJSON 兼容 "auto" 与数组两种形态。
func (c *ChannelList) UnmarshalJSON(b []byte) error {
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "null" || trimmed == "" {
		c.Auto, c.Items = true, nil
		return nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		if s != "auto" {
			return fmt.Errorf("%w: channels string must be \"auto\", got %q", ErrInvalidConfig, s)
		}
		c.Auto, c.Items = true, nil
		return nil
	}
	var items []Channel
	if err := json.Unmarshal(b, &items); err != nil {
		return err
	}
	c.Auto, c.Items = len(items) == 0, items
	return nil
}

// MarshalJSON 回写时保持 "auto" 语义（空数组会让人误以为无通道）。
func (c ChannelList) MarshalJSON() ([]byte, error) {
	if c.IsAuto() {
		return []byte(`"auto"`), nil
	}
	return json.Marshal(c.Items)
}

// Default 返回开箱可用配置：主折线 + 仪表 + 字段表，全部自动绑定通道。
func Default() *Config {
	return &Config{
		Version: "1.0",
		Layout:  Layout{Grid: Grid{Cols: 12, RowHeight: 80}},
		Widgets: []Widget{
			{
				ID: "w_main", Type: TypeLine, Title: "实时波形",
				Position:      Position{X: 0, Y: 0, W: 8, H: 4},
				Channels:      ChannelList{Auto: true},
				WindowSeconds: 60,
				YAxisMode:     AxisAuto,
			},
			{
				ID: "w_gauge", Type: TypeGauge, Title: "最新值",
				Position: Position{X: 8, Y: 0, W: 4, H: 4},
				Channels: ChannelList{Auto: true},
			},
			{
				ID: "w_fields", Type: TypeFieldTable, Title: "原始字段",
				Position: Position{X: 0, Y: 4, W: 12, H: 3},
				Channels: ChannelList{Auto: true},
			},
		},
	}
}

// Load 读取工程内 charts.json；文件不存在时返回 (Default(), false, nil)，
// 由调用方决定是否落盘（首启自动生成）。
func Load(dir string) (*Config, bool, error) {
	path := filepath.Join(dir, FileName)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var cfg Config
	if err := json.Unmarshal(trimBOM(raw), &cfg); err != nil {
		return nil, true, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := cfg.NormalizeAndValidate(); err != nil {
		return nil, true, err
	}
	return &cfg, true, nil
}

// Save 校验后原子写入 charts.json（tmp + rename，避免半截文件被前端读到）。
func Save(dir string, cfg *Config) error {
	_, err := saveWithDigest(dir, cfg)
	return err
}

// saveWithDigest 落盘并返回写入内容的摘要，供热同步判断「这次事件是不是我自己写的」。
func saveWithDigest(dir string, cfg *Config) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("%w: nil config", ErrInvalidConfig)
	}
	if err := cfg.NormalizeAndValidate(); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')

	path := filepath.Join(dir, FileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return digest(data), nil
}

// digest 用 FNV-1a（配置只有几 KB，不需要抗碰撞哈希）。
func digest(data []byte) string {
	var h uint64 = 1469598103934665603
	for _, b := range data {
		h ^= uint64(b)
		h *= 1099511628211
	}
	return fmt.Sprintf("%016x", h)
}

// trimBOM 去掉 UTF-8 BOM：Windows 记事本 / PowerShell 5.1 的
// `Set-Content -Encoding utf8` 都会写 BOM，json.Unmarshal 会直接报错。
// 用户手改 charts.json 时踩到这条的概率很高，必须容错。
func trimBOM(b []byte) []byte {
	return bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
}

// NormalizeAndValidate 补默认值并做结构校验；任何不合法都返回 ErrInvalidConfig。
// 这是 PRD §3.5「所有写入必须通过 schema 校验，失败不落盘」的实现点。
func (c *Config) NormalizeAndValidate() error {
	if c.Version == "" {
		c.Version = "1.0"
	}
	if c.Layout.Grid.Cols == 0 {
		c.Layout.Grid.Cols = 12
	}
	if c.Layout.Grid.RowHeight == 0 {
		c.Layout.Grid.RowHeight = 80
	}
	if c.Layout.Grid.Cols < 1 || c.Layout.Grid.Cols > maxCols {
		return fmt.Errorf("%w: layout.grid.cols must be 1..%d, got %d", ErrInvalidConfig, maxCols, c.Layout.Grid.Cols)
	}
	if c.Layout.Grid.RowHeight < minRowHeight || c.Layout.Grid.RowHeight > maxRowHeight {
		return fmt.Errorf("%w: layout.grid.row_height must be %d..%d", ErrInvalidConfig, minRowHeight, maxRowHeight)
	}
	if len(c.Widgets) > maxWidgets {
		return fmt.Errorf("%w: too many widgets (%d > %d)", ErrInvalidConfig, len(c.Widgets), maxWidgets)
	}

	seen := make(map[string]bool, len(c.Widgets))
	for i := range c.Widgets {
		w := &c.Widgets[i]
		if strings.TrimSpace(w.ID) == "" {
			return fmt.Errorf("%w: widgets[%d].id is required", ErrInvalidConfig, i)
		}
		if seen[w.ID] {
			return fmt.Errorf("%w: duplicate widget id %q", ErrInvalidConfig, w.ID)
		}
		seen[w.ID] = true

		switch w.Type {
		case TypeLine, TypeGauge, TypeIndicator, TypeFieldTable:
		default:
			return fmt.Errorf("%w: widget %q has unknown type %q", ErrInvalidConfig, w.ID, w.Type)
		}
		if strings.TrimSpace(w.Title) == "" {
			w.Title = w.ID
		}

		if w.Position.W < 1 || w.Position.H < 1 {
			return fmt.Errorf("%w: widget %q position w/h must be >= 1", ErrInvalidConfig, w.ID)
		}
		if w.Position.X < 0 || w.Position.Y < 0 {
			return fmt.Errorf("%w: widget %q position x/y must be >= 0", ErrInvalidConfig, w.ID)
		}
		if w.Position.X+w.Position.W > c.Layout.Grid.Cols {
			return fmt.Errorf("%w: widget %q exceeds grid columns (%d+%d > %d)",
				ErrInvalidConfig, w.ID, w.Position.X, w.Position.W, c.Layout.Grid.Cols)
		}

		if w.Type == TypeLine {
			if w.WindowSeconds == 0 {
				w.WindowSeconds = 60
			}
			if w.WindowSeconds < 1 || w.WindowSeconds > 3600 {
				return fmt.Errorf("%w: widget %q window_seconds must be 1..3600", ErrInvalidConfig, w.ID)
			}
			if w.YAxisMode == "" {
				w.YAxisMode = AxisAuto
			}
			if w.YAxisMode != AxisAuto && w.YAxisMode != AxisFixed {
				return fmt.Errorf("%w: widget %q y_axis_mode must be auto|fixed", ErrInvalidConfig, w.ID)
			}
		}

		names := make(map[string]bool, len(w.Channels.Items))
		for j := range w.Channels.Items {
			ch := &w.Channels.Items[j]
			if strings.TrimSpace(ch.Name) == "" {
				return fmt.Errorf("%w: widget %q channels[%d].name is required", ErrInvalidConfig, w.ID, j)
			}
			if names[ch.Name] {
				return fmt.Errorf("%w: widget %q duplicates channel %q", ErrInvalidConfig, w.ID, ch.Name)
			}
			names[ch.Name] = true
			if len(ch.Range) != 0 {
				if len(ch.Range) != 2 {
					return fmt.Errorf("%w: widget %q channel %q range must have 2 numbers", ErrInvalidConfig, w.ID, ch.Name)
				}
				if ch.Range[0] >= ch.Range[1] {
					return fmt.Errorf("%w: widget %q channel %q range lo>=hi", ErrInvalidConfig, w.ID, ch.Name)
				}
			}
			if ch.Digits != nil && (*ch.Digits < 0 || *ch.Digits > 6) {
				return fmt.Errorf("%w: widget %q channel %q display_digits must be 0..6", ErrInvalidConfig, w.ID, ch.Name)
			}
			if ch.AlarmHigh != nil && ch.AlarmLow != nil && *ch.AlarmLow >= *ch.AlarmHigh {
				return fmt.Errorf("%w: widget %q channel %q alarm_low >= alarm_high", ErrInvalidConfig, w.ID, ch.Name)
			}
		}
	}
	return nil
}
