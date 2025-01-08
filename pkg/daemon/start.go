package daemon

import (
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/ahmetozer/sandal/pkg/env"
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

	os.MkdirAll(env.LibDir, 0o0660)
	os.MkdirAll(env.RunDir, 0o0660)

	os.MkdirAll(env.BaseImageDir, 0o0660)
	os.MkdirAll(env.BaseStateDir, 0o0660)

	os.MkdirAll(env.BaseChangeDir, 0o0660)
	os.MkdirAll(env.BaseImmutableImageDir, 0o0660)
	os.MkdirAll(env.BaseRootfsDir, 0o0660)

	wg.Add(2)
	daemonKillRequested := make(chan bool)
	go signalProxy(daemonKillRequested, &wg)
	go daemonControlHealthCheck(daemonKillRequested, &wg)
	wg.Wait()
	checkZombie()
	slog.Info("daemon", slog.String("service", "stopped"))
	return nil
}
