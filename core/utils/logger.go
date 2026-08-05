package utils

import (
	"io"
	"log/slog"
	"os"
)

var logger = slog.New(slog.NewTextHandler(os.Stderr, nil))

func InitLogger(w io.Writer, opts *slog.HandlerOptions) {
	if w == nil {
		w = os.Stderr
	}
	logger = slog.New(slog.NewTextHandler(w, opts))
	slog.SetDefault(logger)
}
