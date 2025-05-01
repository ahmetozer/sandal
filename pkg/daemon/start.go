package daemon

import (
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/cruntime"
	"github.com/ahmetozer/sandal/pkg/container/cruntime/net"
	"github.com/ahmetozer/sandal/pkg/controller"
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

	net.CreateDefaultBridge()

	wg.Add(2)
	daemonKillRequested := make(chan bool)
	go signalProxy(daemonKillRequested, &wg)
	go daemonControlHealthCheck(daemonKillRequested, &wg)
	wg.Wait()

	DaemonPid := os.Getpid()
	conts, err := controller.Containers()
	if err != nil {
		slog.Error("unable to deprovision container during close process", "error", err.Error())
	} else {
		for _, cont := range conts {
			// Do not kill containers, which is executed before daemon
			if cont.HostPid == DaemonPid {
				cruntime.DeRunContainer(cont)
			}
		}
	}

	checkZombie()
	slog.Info("daemon", slog.String("service", "stopped"))
	return nil
}
