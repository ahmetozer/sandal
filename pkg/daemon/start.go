package daemon

import (
	"log/slog"
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
	slog.Info("daemon", "service", "started")

	daemonKillRequested := false
	go signalProxy(&daemonKillRequested)

	daemonControlHealthCheck(&daemonKillRequested)
	checkZombie()
	slog.Info("daemon", slog.String("service", "stopped"))
	return nil
}
