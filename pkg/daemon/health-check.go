package daemon

import (
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/cruntime"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
)

func daemonControlHealthCheck(daemonKillRequested *bool) {
	slog.Info("daemonControlHealthCheck", "service", "started")

	for {
		conts, _ := controller.Containers()
		for c := range conts {
			if *daemonKillRequested {
				slog.Info("daemonControlHealthCheck", "service", "stopped")
				return
			}

			cont := (conts)[c]
			slog.Debug("daemon", slog.String("action", "healthCheck"), slog.Any("len", len((conts))), slog.String("cont", cont.Name))
			if cont.Startup && !cruntime.IsRunning(cont) {
				go recover(cont)
			}
		}
		time.Sleep(3 * time.Second)
	}

}

func recover(cont *config.Config) {
	slog.Debug("daemon", slog.Any("action", "killing old"), slog.String("cont", cont.Name), slog.Any("contpid", cont.ContPid), slog.Any("hostpid", cont.HostPid))

	err := cruntime.Kill(cont.Name, 15, 10)
	if err != nil {
		cruntime.Kill(cont.Name, 9, 1)
	}

	// cruntime.SendSig(cont.HostPid, 9)

	if cont.Status != "killed" {
		slog.Debug("daemon", slog.Any("status", cont.Status), slog.String("cont", cont.Name))
		return
	}

	if len(cont.HostArgs) < 2 {
		slog.Error("daemon", slog.String("error", "unkown arg size"), slog.String("name", cont.Name), slog.String("args", strings.Join(cont.HostArgs, " ")))
		return
	}

	slog.Debug("daemon", slog.Any("action", "starting cont"), slog.Any("args", cont.HostArgs[1:]))
	cmd := exec.Command(env.BinLoc, cont.HostArgs[1:]...)
	// cmd.Stderr = os.Stderr
	// cmd.Stdout = os.Stdout
	err = cmd.Start()
	if err != nil {
		slog.Error("recover", slog.Any("error", err))
	}
	slog.Info("recover", slog.String("cont", cont.Name))

}
