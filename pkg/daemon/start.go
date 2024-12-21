package daemon

import (
	"log/slog"
	"sync"
	"time"
)

type DaemonConfig struct {
	DiskReloadInterval  time.Duration
	HealthCheckInterval time.Duration
}

func (dc DaemonConfig) Start() error {

	var wg sync.WaitGroup

	go func() {
		if dc.DiskReloadInterval == 0 {
			dc.loadByEvent()
		}
	}()
	slog.Info("daemon", "service", "started")

	wg.Add(2)
	daemonKillRequested := make(chan bool)
	go signalProxy(daemonKillRequested, &wg)
	go daemonControlHealthCheck(daemonKillRequested,&wg)
	wg.Wait()
	checkZombie()
	slog.Info("daemon", slog.String("service", "stopped"))
	return nil
}
