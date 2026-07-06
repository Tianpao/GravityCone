//go:build !cli

package main

import (
	"embed"
	"gravitycone/core"
	"log"
	"os"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	application.RegisterEvent[string]("time")
}

func main() {
	// If --service flag is present, run in service-only mode without GUI.
	for _, arg := range os.Args[1:] {
		if arg == "--service" {
			log.Println("Running in service mode (no GUI)")
			return
		}
	}

	natayarkSvc := &core.NatayarkService{}
	scaffoldingSvc := core.NewScaffoldingService(nil) // nil = NilEventEmitter; Wails frontend polls via method calls

	app := application.New(application.Options{
		Name:        "GravityCone",
		Description: "A demo of using raw HTML & CSS",
		Services: []application.Service{
			application.NewService(&core.GreetService{}),
			application.NewService(&core.StunService{}),
			application.NewService(core.NewLanService(nil)),
			application.NewService(natayarkSvc),
			application.NewService(scaffoldingSvc),
			application.NewService(&core.WatermarkService{}),
			application.NewService(&core.SettingsService{}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	// Wire up Wails event emitter now that app exists
	core.InitScaffoldingEmitter(scaffoldingSvc, &wailsEventEmitter{app: app})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "GravityCone",
		Width:     420,
		Height:    680,
		Frameless: true,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		Windows: application.WindowsWindow{
			DisableFramelessWindowDecorations: false,
		},
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              "/",
	})

	go func() {
		for {
			now := time.Now().Format(time.RFC1123)
			app.Event.Emit("time", now)
			time.Sleep(time.Second)
		}
	}()

	if err := natayarkSvc.RestoreSession(); err != nil {
		log.Printf("Warning: failed to restore session: %v", err)
	}

	app.OnShutdown(func() {
		scaffoldingSvc.Cleanup()
	})

	err := app.Run()
	if err != nil {
		log.Fatal(err)
	}
}

// wailsEventEmitter adapts Wails app.Event.Emit to core.EventEmitter.
type wailsEventEmitter struct {
	app *application.App
}

func (e *wailsEventEmitter) Emit(event string, data interface{}) {
	e.app.Event.Emit(event, data)
}
