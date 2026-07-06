package cli

import (
	"encoding/json"
	"io"
	"os"
	"sync"
)

// StdioWriter provides thread-safe JSON line writing to stdout,
// with optional tee to a log file.
type StdioWriter struct {
	mu  sync.Mutex
	enc *json.Encoder
	out *os.File
	tee io.Writer // optional additional writer for logging
}

// NewStdioWriter creates a StdioWriter that writes JSON lines to stdout.
func NewStdioWriter() *StdioWriter {
	w := &StdioWriter{out: os.Stdout}
	w.enc = json.NewEncoder(w.out)
	return w
}

// SetTee sets an additional writer that receives a copy of all output.
func (w *StdioWriter) SetTee(tee io.Writer) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tee = tee
}

// WriteResponse writes a Response as a single JSON line to stdout.
func (w *StdioWriter) WriteResponse(resp Response) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writeLocked(resp)
}

// WriteEvent writes an Event as a single JSON line to stdout.
func (w *StdioWriter) WriteEvent(evt Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writeLocked(evt)
}

// writeLocked marshals and writes a message. Caller must hold w.mu.
func (w *StdioWriter) writeLocked(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	w.out.Write(data)
	w.out.Write([]byte{'\n'})

	if w.tee != nil {
		w.tee.Write(data)
		w.tee.Write([]byte{'\n'})
	}
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
