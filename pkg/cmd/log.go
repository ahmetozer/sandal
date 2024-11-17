package cmd

import (
	"log/slog"
	"os"
	"strings"
)

func SetLogLoggerLevel() {

	switch strings.ToLower(os.Getenv("SANDAL_LOG_LEVEL")) {
	default:
		slog.SetLogLoggerLevel(slog.LevelWarn)
	case "debug":
		slog.SetLogLoggerLevel(slog.LevelDebug)
	case "info":
		slog.SetLogLoggerLevel(slog.LevelInfo)
	case "warn":
		slog.SetLogLoggerLevel(slog.LevelWarn)
	case "error":
		slog.SetLogLoggerLevel(slog.LevelError)
	}
}
