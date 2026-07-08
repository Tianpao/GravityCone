package core

import (
	"io"
	"log/slog"
	"os"
)

// logger is the default structured logger for the core package.
var logger = slog.New(slog.NewTextHandler(os.Stderr, nil))

// InitLogger reconfigures the global logger to write to w with the given options.
// If w is nil, the logger writes to os.Stderr.
func InitLogger(w io.Writer, opts *slog.HandlerOptions) {
	if w == nil {
		w = os.Stderr
	}
	logger = slog.New(slog.NewTextHandler(w, opts))
	slog.SetDefault(logger)
}
