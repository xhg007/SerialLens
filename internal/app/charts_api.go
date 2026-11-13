package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wailsapp/wails/v3/pkg/application"

	"seriallens/internal/charts"
)

// ChartsAPI Wails 薄壳：前台读取/保存 charts.json。
//
// 数据来源单一：磁盘 charts.json 由 internal/charts.Service 管理，
// 前端只做渲染；外部编辑器改动通过 fsnotify → charts:updated 推回前端。
type ChartsAPI struct {
	s *Services
}

// ServiceStartup 注入事件桥接（install 幂等）。
func (c *ChartsAPI) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	c.s.install(application.Get())
	return nil
}

// GetChartsConfig 返回当前工程的图表配置；未打开工程时返回 nil（不报错，
// 避免启动阶段刷一条无意义的 binding 错误日志）。
func (c *ChartsAPI) GetChartsConfig() (map[string]any, error) {
	cfg := c.s.charts.Config()
	if cfg == nil {
		return nil, nil
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SaveChartsConfig 保存并广播（拖拽布局、AI 生成、手工编辑回写都走这里）。
// 校验失败直接返回错误且不落盘（PRD §3.5 写入规则）。
func (c *ChartsAPI) SaveChartsConfig(raw map[string]any) error {
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var cfg charts.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("invalid charts config: %w", err)
	}
	return c.s.charts.SaveConfig(&cfg)
}

// ReloadCharts 强制从磁盘重读（前端手动刷新按钮）。
func (c *ChartsAPI) ReloadCharts() error {
	return c.s.charts.Reload()
}

// ResetChartsToDefault 把 charts.json 重置为默认模板（主折线 + 仪表 + 字段表）。
func (c *ChartsAPI) ResetChartsToDefault() error {
	return c.s.charts.SaveConfig(charts.Default())
}
