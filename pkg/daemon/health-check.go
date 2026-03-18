//go:build linux

package daemon

import (
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/cruntime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

func daemonControlHealthCheck(daemonKillRequested chan bool, wg *sync.WaitGroup) {
	slog.Info("daemonControlHealthCheck", "service", "started")

	defer slog.Info("daemonControlHealthCheck", "service", "stopped")
	defer wg.Done()

	for {
		select {
		case <-daemonKillRequested:
			return
		case <-time.After(3 * time.Second):
			conts, err := controller.Containers()
			if err != nil {
				slog.Warn("unable to get containers", "err", err.Error())
			}
			for c := range conts {
				cont := (conts)[c]

				isRunning, err := cruntime.IsPidRunning(cont.ContPid)
				if err != nil {
					slog.Warn("unable to get container status", "cont", cont.Name, "err", err.Error())
				}
				slog.Debug("daemon", slog.String("action", "healthCheck"), slog.Any("len", len((conts))), slog.String("cont", cont.Name), slog.Bool("running", isRunning))
				if !isRunning {
					if cont.Startup {
						go contRecover(cont)
					} else {
						slog.Debug("daemon", slog.String("cont", cont.Name), slog.String("msg", "recovering bypassed"))
					}
				}
			}
		}
	}

}

func contRecover(cont *config.Config) {
	if cont.Status == "stop" {
		return
	}
	slog.Debug("daemon", slog.Any("action", "killing old"), slog.String("cont", cont.Name), slog.Any("contpid", cont.ContPid), slog.Any("hostpid", cont.HostPid))

	err := cruntime.Kill(cont, 15, 10)
	if err != nil {
		cruntime.Kill(cont, 9, 0)
	}

	if !(cont.Status == "stop" || cont.Status == "killed") {
		slog.Debug("daemon", slog.Any("status", cont.Status), slog.String("cont", cont.Name))
		return
	}

	// Re-read the latest config from the controller. Between the health-check
	// detecting a dead PID and reaching this point, the user may have run
	// "sandal kill; sandal run -name X ..." which updates the config with new
	// parameters. Using the fresh config ensures we start with the right args.
	latest, err := controller.GetContainer(cont.Name)
	if err != nil {
		slog.Error("recover", slog.String("cont", cont.Name), slog.Any("error", err))
		return
	}

	// If the container is already running (started by another path),
	// skip recovery to avoid duplicate instances.
	if isRunning, _ := cruntime.IsPidRunning(latest.ContPid); isRunning {
		slog.Debug("daemon", slog.String("cont", cont.Name), slog.String("msg", "already running, skipping recovery"))
		return
	}

	if len(latest.HostArgs) < 2 {
		slog.Error("daemon", slog.String("error", "unkown arg size"), slog.String("name", latest.Name), slog.String("args", strings.Join(latest.HostArgs, " ")))
		return
	}

	err = cruntime.Run(latest)
	if err != nil {
		slog.Error("recover", slog.Any("error", err))
	}
	slog.Info("recover", slog.String("cont", latest.Name))

}
