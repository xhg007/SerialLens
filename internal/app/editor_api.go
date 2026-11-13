package app

import (
	"context"
	"fmt"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"seriallens/internal/lua"
	"seriallens/internal/scripts"
)

// EditorAPI Wails 薄壳：脚本文件 CRUD、保存即生效、API 清单、单帧试运行（PRD §F4）。
//
// 所有落盘与热装载逻辑在 internal/scripts，这里只做「工程上下文 + 前端协议」的适配。
type EditorAPI struct {
	s *Services
}

// ServiceStartup 启动时注入事件桥接。
func (e *EditorAPI) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	e.s.install(application.Get())
	return nil
}

// ListScripts 返回 scripts/ 目录下的脚本（活动脚本置顶）。
func (e *EditorAPI) ListScripts() ([]scripts.File, error) {
	return e.s.scripts.List()
}

// ReadScript 读取脚本内容；ack 里带上活动标记，前端据此决定是否重载解析管线。
func (e *EditorAPI) ReadScript(name string) (map[string]any, error) {
	content, err := e.s.scripts.Read(name)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"name":    name,
		"content": content,
		"active":  e.s.scripts.Active() == name,
	}, nil
}

// SaveScript 写盘 + 编译校验 + （活动脚本）热装载，返回诊断与耗时。
func (e *EditorAPI) SaveScript(name, content string) (scripts.SaveResult, error) {
	if _, _, ok := e.s.Project.svc.Current(); !ok {
		return scripts.SaveResult{}, fmt.Errorf("no project open")
	}
	return e.s.scripts.Save(name, content)
}

// CreateScript 新建脚本（空模板）。
func (e *EditorAPI) CreateScript(name string) error {
	return e.s.scripts.Create(name)
}

// DuplicateScript 复制脚本。
func (e *EditorAPI) DuplicateScript(from, to string) error {
	return e.s.scripts.Duplicate(from, to)
}

// DeleteScript 删除脚本（活动脚本拒绝删除）。
func (e *EditorAPI) DeleteScript(name string) error {
	return e.s.scripts.Remove(name)
}

// RenameScript 重命名脚本；若改的是活动脚本，同步 project.json 与新名字的字节码缓存。
func (e *EditorAPI) RenameScript(oldName, newName string) error {
	wasActive := e.s.scripts.Active() == oldName
	if err := e.s.scripts.Rename(oldName, newName); err != nil {
		return err
	}
	if !wasActive {
		return nil
	}
	if err := e.s.Project.svc.SetActiveScript(newName); err != nil {
		return err
	}
	return e.s.scripts.SetActive(newName)
}

// SetActiveScript 切换活动脚本：写回 project.json 并立即装载。
func (e *EditorAPI) SetActiveScript(name string) error {
	if err := e.s.scripts.SetActive(name); err != nil {
		return err
	}
	return e.s.Project.svc.SetActiveScript(name)
}

// GetApiManifest 返回内置 API 清单（Monaco 自动补全与悬停文档的数据源）。
func (e *EditorAPI) GetApiManifest() []lua.APIDoc {
	return lua.APIManifest()
}

// TestRunScript 用给定源码试跑一帧 hex，**不触碰在线脚本与串口**。
//
// 用一次性 Runtime 执行：send_bytes sink 被替换成错误，沙箱试跑不可能往串口写数据。
func (e *EditorAPI) TestRunScript(source, hexStr string) (map[string]any, error) {
	raw, err := decodeHex(hexStr)
	if err != nil {
		return nil, err
	}
	name := e.s.scripts.Active()
	if name == "" {
		name = "test.lua"
	}

	rt := lua.New(lua.Options{
		Sinks: lua.Sinks{
			Send: func([]byte) error { return fmt.Errorf("send_bytes is disabled during test run") },
		},
	})
	defer rt.Close()

	if err := rt.LoadScript(name, []byte(source)); err != nil {
		d := lua.DiagnosticFromError(name, err)
		return map[string]any{"ok": false, "diagnostic": d}, nil
	}

	start := time.Now()
	fields, points, err := rt.CallOnFrame(raw)
	duration := time.Since(start).Microseconds()
	if err != nil {
		d := lua.DiagnosticFromError(name, err)
		return map[string]any{"ok": false, "diagnostic": d, "duration_us": duration}, nil
	}

	pts := make([]map[string]any, 0, len(points))
	for _, p := range points {
		pts = append(pts, map[string]any{"ch": p.Channel, "v": p.Value})
	}
	return map[string]any{
		"ok":          true,
		"fields":      fields,
		"points":      pts,
		"duration_us": duration,
	}, nil
}
