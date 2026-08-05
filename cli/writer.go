//go:build !et_ffi

package cli

import (
	"encoding/json"
	"io"
	"os"
	"sync"

	"gravitycone/core/utils"
)

// StdioWriter provides thread-safe JSON line writing to stdout,
// with optional tee to a log file.
type StdioWriter struct {
	mu  sync.Mutex
	out *os.File
	tee io.Writer
}

func NewStdioWriter() *StdioWriter {
	return &StdioWriter{out: os.Stdout}
}

func (w *StdioWriter) SetTee(tee io.Writer) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tee = tee
}

func (w *StdioWriter) WriteResponse(resp Response) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writeLocked(resp)
}

func (w *StdioWriter) WriteEvent(evt Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writeLocked(evt)
}

// writeLocked marshals and writes a message. Caller must hold w.mu.
func (w *StdioWriter) writeLocked(v any) {
	data, _ := json.Marshal(v)
	w.out.Write(data)
	w.out.Write([]byte{'\n'})

	if w.tee != nil {
		w.tee.Write(data)
		w.tee.Write([]byte{'\n'})
	}
}

type StdioEventEmitter struct {
	writer *StdioWriter
}

var _ utils.EventEmitter = (*StdioEventEmitter)(nil)

func NewStdioEventEmitter(writer *StdioWriter) *StdioEventEmitter {
	return &StdioEventEmitter{writer: writer}
}

func (e *StdioEventEmitter) Emit(event string, data any) {
	e.writer.WriteEvent(Event{Event: event, Data: data})
}
