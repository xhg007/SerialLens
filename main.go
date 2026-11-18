package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"

	"seriallens/internal/app"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

//go:embed build/trayicon.png
var trayIcon []byte

func main() {
	services := app.NewServices()
	defer services.Shutdown()

	desktop := app.NewDesktop()

	wailsApp := application.New(application.Options{
		Name:        "SerialLens",
		Description: "智能串口协议调试工具",
		Icon:        appIcon,
		Services: []application.Service{
			application.NewService(services.Project),
			application.NewService(services.Serial),
			application.NewService(services.Export),
			application.NewService(services.Parser),
			application.NewService(services.Charts),
			application.NewService(services.Editor),
			application.NewService(services.AI),
			application.NewService(desktop),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		// 单实例：串口连接是独占资源，双开必然抢端口。
		// 第二次启动时通知首实例把主窗口拉到前台，自身退出。
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.seriallens.app",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				desktop.FocusMainWindow()
			},
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	// 无边框：标题栏/窗口按钮由前端自绘（components/TitleBar.tsx）。
	// Windows 上保留 resizable 的系统装饰（阴影、Win11 圆角），只去掉原生标题栏。
	wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "SerialLens",
		Width:            1280,
		Height:           800,
		MinWidth:         960,
		MinHeight:        600,
		Frameless:        true,
		BackgroundColour: application.NewRGB(15, 19, 26),
		Windows: application.WindowsWindow{
			Theme: application.Dark,
		},
		// 右键菜单由前端 ContextMenu.tsx 统一接管（只有复制/粘贴/全选）。
		// 开发模式 Wails 仍会强制打开原生菜单，前端在 contextmenu 里 preventDefault 压掉；
		// 生产构建靠这里关掉原生菜单，保证任何情况下都不会出现 Inspect。
		DefaultContextMenuDisabled: true,
		URL:                        "/",
	})

	desktop.Init(trayIcon)

	if err := wailsApp.Run(); err != nil {
		log.Fatal(err)
	}
}
