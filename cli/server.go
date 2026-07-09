package cli

import (
	"bufio"
	"encoding/json"
	"gravitycone/core"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"
)

const version = "1.0.0"

// Run starts the CLI stdio server. It reads JSON requests from stdin,
// dispatches them to core services, and writes responses/events to stdout.
// peers overrides the default EasyTier public peer list.
// vendorPrefix is prepended to the vendor string in room operations.
// motd is the custom MOTD for LAN broadcast (empty uses the default).
func Run(peers []string, vendorPrefix string, motd string) {
	// Resolve logs directory next to the CLI executable
	logsDir, stdioLogPath, etLogPath, gccoreLogPath, err := resolveLogPaths()
	if err != nil {
		slog.Error("failed to resolve log paths", "error", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		slog.Error("failed to create logs directory", "error", err)
		os.Exit(1)
	}

	// Redirect Go slog to gccore.log (no terminal output)
	gccoreLog, err := os.OpenFile(gccoreLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		slog.Error("failed to open gccore.log", "error", err)
		os.Exit(1)
	}
	defer gccoreLog.Close()
	core.InitLogger(gccoreLog, &slog.HandlerOptions{AddSource: false})

	// Redirect EasyTier logs to file
	core.SetEasyTierLogOutput(etLogPath)

	// Override EasyTier peers if provided
	if len(peers) > 0 {
		core.SetPublicPeers(peers)
		slog.Info("Using custom peers", "peers", peers)
	}

	// Ensure EasyTier binaries are available (auto-download if missing)
	if err := core.EnsureEasyTier(); err != nil {
		slog.Warn("EasyTier auto-download failed", "error", err)
	}

	// Set up services
	writer := NewStdioWriter()
	emitter := NewStdioEventEmitter(writer)

	stunSvc := &core.StunService{}
	lanSvc := core.NewLanService(emitter)
	scaffoldingSvc := core.NewScaffoldingService(emitter)

	shutdownCh := make(chan struct{})
	handler := NewHandler(stunSvc, lanSvc, scaffoldingSvc, writer, shutdownCh, vendorPrefix, motd)

	// Open stdio log file
	stdioLog, err := os.OpenFile(stdioLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		slog.Warn("failed to open stdio.log", "error", err)
	} else {
		defer stdioLog.Close()
		writer.SetTee(stdioLog)
	}

	// Emit system.ready
	writer.WriteEvent(Event{
		Event: "system.ready",
		Data:  map[string]string{"version": version},
	})

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	var wg sync.WaitGroup

	// Read loop on stdin
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if stdioLog != nil {
				stdioLog.WriteString("> " + line + "\n")
			}
			var req Request
			if err := json.Unmarshal([]byte(line), &req); err != nil {
				writer.WriteResponse(errorResponse(0, ErrInvalidParams, err.Error()))
				continue
			}
			wg.Add(1)
			go func(r Request) {
				defer wg.Done()
				handler.Handle(r)
			}(req)
		}
		// stdin closed, wait for in-flight requests then trigger shutdown
		go func() {
			wg.Wait()
			handler.shutdownOnce.Do(func() {
				close(shutdownCh)
			})
		}()
	}()

	// Wait for shutdown signal
	select {
	case <-shutdownCh:
	case <-sigCh:
	}

	// Wait briefly for any final response writes to flush
	wg.Wait()
	time.Sleep(50 * time.Millisecond)

	// Cleanup
	scaffoldingSvc.Cleanup()
	lanSvc.StopDiscovery()
}

// resolveLogPaths returns (logsDir, stdioLogPath, etLogPath, gccoreLogPath).
// The logs directory is placed next to the CLI executable.
func resolveLogPaths() (string, string, string, string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", "", "", "", err
	}
	dir := filepath.Dir(exe)
	// Resolve symlinks
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", "", "", "", err
	}
	logsDir := filepath.Join(dir, "logs")
	return logsDir, filepath.Join(logsDir, "stdio.log"), filepath.Join(logsDir, "easytier.log"), filepath.Join(logsDir, "gccore.log"), nil
}
