// Package logger builds the structured JSON logger used by every component.
package logger

import (
	"io"
	"log/slog"
	"strings"
)

// New returns a JSON slog.Logger at the given level (debug|info|warn|error).
func New(w io.Writer, level string) *slog.Logger {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: l}))
}
