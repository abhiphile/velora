package logger

import (
	"io"
	"log/slog"
)

// New creates the structured logger shared by command and runtime boundaries.
func New(output io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(output, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
