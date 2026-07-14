//go:build !cli

package main

import (
	"embed"
	"gravitycone/core/app"
	"gravitycone/core/app/account"
	"gravitycone/core/easytier"
	"gravitycone/core/minecraft"
	"gravitycone/core/protocol/scaffolding"
	"gravitycone/core/utils"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Microsoft OAuth2 credentials — set via -ldflags at build time.
// Example: go build -ldflags "-X main.msClientID=xxx -X main.msClientSecret=yyy"
// Falls back to MS_CLIENT_ID / MS_CLIENT_SECRET env vars (or .env file) if empty.
var (
	msClientID     string
	msClientSecret string
)

// Naids OAuth2 credentials — same pattern as Microsoft credentials above.
// Falls back to NAIDS_CLIENT_ID / NAIDS_CLIENT_SECRET env vars (or .env file) if empty.
var (
	naidsClientID     string
	naidsClientSecret string
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	application.RegisterEvent[easytier.DownloadProgressData]("download.progress")
	application.RegisterEvent[scaffolding.PlayerInfo]("room.player_joined")
	application.RegisterEvent[scaffolding.PlayerInfo]("room.player_left")
	application.RegisterEvent[map[string]string]("room.closed")
	application.RegisterEvent[map[string]string]("room.disconnected")
	application.RegisterEvent[minecraft.LanServer]("lan.server_found")
	application.RegisterEvent[map[string]interface{}]("lan.server_lost")
}

func main() {
	// Load .env file — try working directory first, then executable directory.
	godotenv.Load(".env")
	if exe, err := os.Executable(); err == nil {
		godotenv.Load(filepath.Join(filepath.Dir(exe), ".env"))
	}

	// Resolve MS credentials: ldflags > env vars
	clientID := msClientID
	clientSecret := msClientSecret
	if clientID == "" {
		clientID = os.Getenv("MS_CLIENT_ID")
	}
	if clientSecret == "" {
		clientSecret = os.Getenv("MS_CLIENT_SECRET")
	}

	// Resolve Naids credentials: ldflags > env vars
	// client_id "gravitycone" is the public identifier; client_secret must be injected at build time.
	naidsID := naidsClientID
	naidsSecret := naidsClientSecret
	if naidsID == "" {
		naidsID = os.Getenv("NAIDS_CLIENT_ID")
		if naidsID == "" {
			naidsID = "gravitycone"
		}
	}
	if naidsSecret == "" {
		naidsSecret = os.Getenv("NAIDS_CLIENT_SECRET")
	}

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
			utils.InitLogger(f, nil)
		}
	}
	// Redirect EasyTier logs to file too
	if exe, err := os.Executable(); err == nil {
		logDir := filepath.Join(filepath.Dir(exe), "logs")
		easytier.SetEasyTierLogOutput(filepath.Join(logDir, "easytier.log"))
	}

	natayarkSvc := account.NewNatayarkService(naidsID, naidsSecret)
	minecraftSvc := account.NewMinecraftService(clientID, clientSecret)
	lanSvc := minecraft.NewLanService(nil)
	scaffoldingSvc := scaffolding.NewScaffoldingService(nil) // nil = NilEventEmitter; Wails frontend polls via method calls

	app := application.New(application.Options{
		Name:        "GravityCone",
		Description: "A demo of using raw HTML & CSS",
		Services: []application.Service{
			application.NewService(&easytier.StunService{}),
			application.NewService(lanSvc),
			application.NewService(natayarkSvc),
			application.NewService(minecraftSvc),
			application.NewService(scaffoldingSvc),
			application.NewService(&app.WatermarkService{}),
			application.NewService(&easytier.SettingsService{}),
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
	lanSvc.SetEventEmitter(wailsEmitter)
	scaffolding.InitScaffoldingEmitter(scaffoldingSvc, wailsEmitter)
	easytier.SetEnsureEasyTierEmitter(wailsEmitter)

	// Ensure EasyTier binaries are available (auto-download if missing).
	// Run in background so the window appears immediately.
	go func() {
		if err := easytier.EnsureEasyTier(); err != nil {
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

	if err := natayarkSvc.RestoreSession(); err != nil {
		slog.Warn("failed to restore session", "error", err)
	}
	if err := minecraftSvc.RestoreSession(); err != nil {
		slog.Warn("failed to restore Minecraft session", "error", err)
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

// wailsEventEmitter adapts Wails app.Event.Emit to utils.EventEmitter.
type wailsEventEmitter struct {
	app *application.App
}

func (e *wailsEventEmitter) Emit(event string, data interface{}) {
	e.app.Event.Emit(event, data)
}
