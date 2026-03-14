package cmd

import (
	"log/slog"
	"os"
	"strings"
)

// KernelLogLevel maps the sandal log level to a Linux kernel loglevel string.
// debug → 7 (all messages), info → 4 (warn+), warn → 2 (crit+), error → 0 (emerg only)
var KernelLogLevel = "4"

func init() {
	setLogLoggerLevel()
}

func setLogLoggerLevel() {

	var (
		level     slog.Leveler
		AddSource bool
	)
	switch strings.ToLower(os.Getenv("SANDAL_LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
		AddSource = true
		KernelLogLevel = "7"
	case "", "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
		KernelLogLevel = "2"
	case "error":
		level = slog.LevelError
		KernelLogLevel = "0"
	default:
		slog.Warn("SetLogLoggerLevel", slog.String("error", "unknown log level"), slog.String("level", os.Getenv("SANDAL_LOG_LEVEL")), slog.String("env", "SANDAL_LOG_LEVEL"), slog.Any("logLevels", []string{"info", "debug", "warn", "error"}))
		level = slog.LevelInfo
	}

	slog.SetDefault(
		slog.New(slog.NewJSONHandler(os.Stderr,
			&slog.HandlerOptions{
				AddSource: AddSource,
				Level:     level,
				// ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				// 	if a.Key == slog.SourceKey {
				// 		source, _ := a.Value.Any().(*slog.Source)
				// 		if source != nil {
				// 			// source.File = filepath.Base(source.File)
				// 			source.File = " " + source.Function
				// 		}
				// 	}
				// 	return a
				// },
			},
		)),
	)

}
