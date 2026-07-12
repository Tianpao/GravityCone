//go:build !cli

package main

import (
	"embed"
	"gravitycone/core"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	application.RegisterEvent[string]("time")
	application.RegisterEvent[core.DownloadProgressData]("download.progress")
}

func main() {
	// If --service flag is present, run in service-only mode without GUI.
	for _, arg := range os.Args[1:] {
		if arg == "--service" {
			slog.Info("Running in service mode (no GUI)")
			return
		}
	}

	// Redirect GravityCone logs to file (separate from EasyTier noise)
	if exe, err := os.Executable(); err == nil {
		logDir := filepath.Join(filepath.Dir(exe), "logs")
		os.MkdirAll(logDir, 0755)
		if f, err := os.OpenFile(filepath.Join(logDir, "gravitycone.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644); err == nil {
			core.InitLogger(f, nil)
		}
	}
	// Redirect EasyTier logs to file too
	if exe, err := os.Executable(); err == nil {
		logDir := filepath.Join(filepath.Dir(exe), "logs")
		core.SetEasyTierLogOutput(filepath.Join(logDir, "easytier.log"))
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

	// Wire up Wails event emitters now that app exists
	wailsEmitter := &wailsEventEmitter{app: app}
	core.InitScaffoldingEmitter(scaffoldingSvc, wailsEmitter)
	core.SetEnsureEasyTierEmitter(wailsEmitter)

	// Ensure EasyTier binaries are available (auto-download if missing).
	// Run in background so the window appears immediately.
	go func() {
		if err := core.EnsureEasyTier(); err != nil {
			slog.Warn("EasyTier auto-download failed", "error", err)
		}
	}()

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
		slog.Warn("failed to restore session", "error", err)
	}

	app.OnShutdown(func() {
		scaffoldingSvc.Cleanup()
	})

	err := app.Run()
	if err != nil {
		slog.Error("app.Run failed", "error", err)
		os.Exit(1)
	}
}

// wailsEventEmitter adapts Wails app.Event.Emit to core.EventEmitter.
type wailsEventEmitter struct {
	app *application.App
}

func (e *wailsEventEmitter) Emit(event string, data interface{}) {
	e.app.Event.Emit(event, data)
}
