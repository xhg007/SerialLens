package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"

	"seriallens/internal/app"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	services := app.NewServices()
	defer services.Shutdown()

	app := application.New(application.Options{
		Name:        "SerialLens",
		Description: "智能串口协议调试工具",
		Services: []application.Service{
			application.NewService(services.Project),
			application.NewService(services.Serial),
			application.NewService(services.Parser),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "SerialLens",
		Width:  1280,
		Height: 800,
		BackgroundColour: application.NewRGB(15, 19, 26),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
