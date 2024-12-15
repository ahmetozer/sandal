package cmd

import (
	"log/slog"
	"os"
	"strings"
)

func init() {
	setLogLoggerLevel()
}

func setLogLoggerLevel() {

	switch strings.ToLower(os.Getenv("SANDAL_LOG_LEVEL")) {
	case "info":
		slog.SetLogLoggerLevel(slog.LevelInfo)
	case "", "debug":
		slog.SetLogLoggerLevel(slog.LevelDebug)
	case "warn":
		slog.SetLogLoggerLevel(slog.LevelWarn)
	case "error":
		slog.SetLogLoggerLevel(slog.LevelError)
	default:
		slog.Warn("SetLogLoggerLevel", slog.String("error", "unknown log level"), slog.String("level", os.Getenv("SANDAL_LOG_LEVEL")), slog.String("env", "SANDAL_LOG_LEVEL"), slog.Any("logLevels", []string{"info", "debug", "warn", "error"}))
		slog.SetLogLoggerLevel(slog.LevelInfo)
	}

}
