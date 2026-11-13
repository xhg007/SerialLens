package app

import (
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	wailsevents "github.com/wailsapp/wails/v3/pkg/events"
)

// Desktop 桌面外壳：无边框窗口（自绘标题栏）+ 系统托盘。
//
// 三件事只在这里做，避免散落到 main 与前端：
//  1. 关闭按钮 = 收进托盘（串口工具常驻，误关会丢掉现场与串口会话）；
//  2. 托盘菜单「退出」才真正退出，其余窗口控制都给前端按钮用；
//  3. 窗口隐藏后 WebView 仍然活着，串口连接与解析状态不中断。
//
// 方法会被 Wails 绑定成前端 API（desktop.ts），因此保持无参、可序列化返回。
type Desktop struct {
	app  *application.App
	win  application.Window
	tray *application.SystemTray

	quitting atomic.Bool
	inited   bool
}

// NewDesktop 建一个空壳，窗口创建完再 Init。
func NewDesktop() *Desktop { return &Desktop{} }

// Init 窗口创建后接线：关闭拦截 + 托盘。由 main 在 Run 之前调用一次。
func (d *Desktop) Init(icon []byte) {
	if d == nil || d.inited {
		return
	}
	app := application.Get()
	if app == nil {
		return
	}
	win, ok := app.Window.GetByName("main")
	if !ok {
		win = app.Window.Current()
	}
	d.inited = true
	d.app = app
	d.win = win

	// 点关闭只收进托盘；quitting 由托盘「退出」置位后放行
	if win != nil {
		win.RegisterHook(wailsevents.Common.WindowClosing, func(e *application.WindowEvent) {
			if d.quitting.Load() {
				return
			}
			e.Cancel()
			d.HideToTray()
		})
	}

	if app == nil {
		return
	}
	menu := application.NewMenu()
	menu.Add("显示主窗口").OnClick(func(*application.Context) { d.ShowWindow() })
	menu.AddSeparator()
	menu.Add("退出 SerialLens").OnClick(func(*application.Context) { d.Quit() })

	tray := app.SystemTray.New()
	if len(icon) > 0 {
		tray.SetIcon(icon)
	}
	tray.SetTooltip("SerialLens — 智能串口协议调试工具")
	tray.SetMenu(menu)
	tray.OnClick(func() { d.ToggleWindow() })
	tray.OnDoubleClick(func() { d.ShowWindow() })
	tray.Show()
	d.tray = tray
}

// Minimise 最小化到任务栏。
func (d *Desktop) Minimise() {
	if d != nil && d.win != nil {
		d.win.Minimise()
	}
}

// ToggleMaximise 切换最大化，返回切换后的状态（前端据此换图标）。
func (d *Desktop) ToggleMaximise() bool {
	if d == nil || d.win == nil {
		return false
	}
	d.win.ToggleMaximise()
	return d.win.IsMaximised()
}

// IsMaximised 当前是否最大化。
func (d *Desktop) IsMaximised() bool {
	if d == nil || d.win == nil {
		return false
	}
	return d.win.IsMaximised()
}

// CloseToTray 标题栏 × 的落点：收进托盘，不退出。
func (d *Desktop) CloseToTray() { d.HideToTray() }

// HideToTray 隐藏窗口，进程与串口会话继续。
func (d *Desktop) HideToTray() {
	if d != nil && d.win != nil {
		d.win.Hide()
	}
}

// ShowFromTray 从托盘恢复并聚焦（托盘菜单、托盘双击、前端都用它）。
func (d *Desktop) ShowFromTray() { d.ShowWindow() }

// ShowWindow 显示并前置窗口。
func (d *Desktop) ShowWindow() {
	if d == nil || d.win == nil {
		return
	}
	d.win.Show()
	d.win.Focus()
}

// ToggleWindow 托盘单击：可见就藏，不可见就显示。
func (d *Desktop) ToggleWindow() {
	if d == nil || d.win == nil {
		return
	}
	if d.win.IsVisible() {
		d.win.Hide()
		return
	}
	d.ShowWindow()
}

// Quit 真正退出（托盘菜单用，也是前端「退出」的唯一出口）。
func (d *Desktop) Quit() {
	if d == nil {
		return
	}
	d.quitting.Store(true)
	if d.app != nil {
		d.app.Quit()
	}
}
