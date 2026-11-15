package logger

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

func New(level, format string) (*slog.Logger, error) {
	var slogLevel slog.Level

	switch strings.ToLower(level) {
	case "debug":
		slogLevel = slog.LevelDebug
	case "info":
		slogLevel = slog.LevelInfo
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		return nil, fmt.Errorf("unsupported log level %q", level)
	}

	opts := &slog.HandlerOptions{
		Level: slogLevel,
	}

	var handler slog.Handler

	switch strings.ToLower(format) {
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	case "text":
		handler = slog.NewTextHandler(os.Stdout, opts)
	default:
		return nil, fmt.Errorf("unsupported log format %q", format)
	}

	return slog.New(handler), nil
}
