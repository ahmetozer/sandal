package daemon

import (
	"log/slog"
	"os"
	"time"
)

type DaemonConfig struct {
	DiskReloadInterval  time.Duration
	HealthCheckInterval time.Duration
}

func (dc DaemonConfig) Start() error {
	go func() {
		if dc.DiskReloadInterval == 0 {
			dc.loadByEvent()
		}
	}()

	slog.Info("daemon", slog.String("message", "sandal daemon service started"))
	if _, err := os.Stat("/etc/init.d/sandal"); err != nil {
		slog.Info("daemon", slog.String("message", `You can enable sandal daemon at startup with 'sandal daemon -install' command.`+
			`It will install service files for systemd and runit`))
	}

	daemonKillRequested := false
	go signalProxy(&daemonKillRequested)

	daemonControlHealthCheck(&daemonKillRequested)
	slog.Info("daemon", slog.String("message", "exiting daemon"))
	return nil
}
