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

	var (
		level     slog.Leveler
		AddSource bool
	)
	switch strings.ToLower(os.Getenv("SANDAL_LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
		AddSource = true
	case "", "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		slog.Warn("SetLogLoggerLevel", slog.String("error", "unknown log level"), slog.String("level", os.Getenv("SANDAL_LOG_LEVEL")), slog.String("env", "SANDAL_LOG_LEVEL"), slog.Any("logLevels", []string{"info", "debug", "warn", "error"}))
		level = slog.LevelInfo
	}

	slog.SetDefault(
		slog.New(slog.NewTextHandler(os.Stderr,
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
