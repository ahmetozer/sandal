//go:build linux

package daemon

import (
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/host"
	"github.com/ahmetozer/sandal/pkg/container/net"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/modprobe"
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

	for _, mod := range []string{"vhost_net", "vhost_vsock"} {
		if err := modprobe.Load(mod); err != nil {
			slog.Warn("modprobe", slog.String("module", mod), slog.Any("err", err))
		}
	}

	// Rehydrate port-forward listeners for already-running containers
	// before starting the supervisor loops. After a daemon crash the
	// container PIDs are still alive but their listeners died with the
	// previous daemon process; this loop reattaches without touching
	// the containers themselves. Health-check-driven recovery (which
	// goes through crun and registers via Forwards.Add) will replace
	// any sessions installed here for containers it later restarts.
	host.RehydrateAllForwards()

	wg.Add(2)
	daemonKillRequested := make(chan bool)
	go signalProxy(daemonKillRequested, &wg)
	go daemonControlHealthCheck(daemonKillRequested, &wg)
	wg.Wait()

	// Release port-forward listeners before tearing down containers so
	// in-flight relays don't dial into netns that's about to disappear.
	host.Forwards.StopAll()

	DaemonPid := os.Getpid()
	conts, err := controller.Containers()
	if err != nil {
		slog.Error("unable to deprovision container during close process", "error", err.Error())
	} else {
		for _, cont := range conts {
			// Do not kill containers, which is executed before daemon
			if cont.HostPid == DaemonPid {
				host.DeRunContainer(cont)
			}
		}
	}

	checkZombie()
	slog.Info("daemon", slog.String("service", "stopped"))
	return nil
}
