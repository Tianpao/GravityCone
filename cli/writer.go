package cli

import (
	"encoding/json"
	"os"
	"sync"
)

// StdioWriter provides thread-safe JSON line writing to stdout.
type StdioWriter struct {
	mu   sync.Mutex
	enc  *json.Encoder
	out  *os.File
}

// NewStdioWriter creates a StdioWriter that writes JSON lines to stdout.
func NewStdioWriter() *StdioWriter {
	w := &StdioWriter{out: os.Stdout}
	w.enc = json.NewEncoder(w.out)
	return w
}

// WriteResponse writes a Response as a single JSON line to stdout.
func (w *StdioWriter) WriteResponse(resp Response) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.enc.Encode(resp)
}

// WriteEvent writes an Event as a single JSON line to stdout.
func (w *StdioWriter) WriteEvent(evt Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.enc.Encode(evt)
}

// StdioEventEmitter implements core.EventEmitter by writing events to stdout.
type StdioEventEmitter struct {
	writer *StdioWriter
}

// NewStdioEventEmitter creates an EventEmitter that pushes CLI events to stdout.
func NewStdioEventEmitter(writer *StdioWriter) *StdioEventEmitter {
	return &StdioEventEmitter{writer: writer}
}

func (e *StdioEventEmitter) Emit(event string, data interface{}) {
	e.writer.WriteEvent(Event{Event: event, Data: data})
}
