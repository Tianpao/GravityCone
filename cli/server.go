package cli

import (
	"bufio"
	"encoding/json"
	"gravitycone/core"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"
)

const version = "1.0.0"

// Run starts the CLI stdio server. It reads JSON requests from stdin,
// dispatches them to core services, and writes responses/events to stdout.
func Run() {
	log.SetOutput(os.Stderr)
	log.SetPrefix("[gravitycone-cli] ")

	// Set up services
	writer := NewStdioWriter()
	emitter := NewStdioEventEmitter(writer)

	stunSvc := &core.StunService{}
	lanSvc := core.NewLanService(emitter)
	scaffoldingSvc := core.NewScaffoldingService(emitter)

	shutdownCh := make(chan struct{})
	handler := NewHandler(stunSvc, lanSvc, scaffoldingSvc, writer, shutdownCh)

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
