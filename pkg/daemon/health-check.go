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
			conts, _ := controller.Containers()
			for c := range conts {
				cont := (conts)[c]
				cont, err := controller.GetContainer(cont.Name)
				if err != nil {
					slog.Error(err.Error())
					continue
				}
				isRunning := cruntime.IsRunning(cont)
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
	// defer func() {
	// 	if r := recover(); r != nil {
	// 		slog.Debug("recover", slog.Any("err", r))
	// 	}
	// }()

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

	if len(cont.HostArgs) < 2 {
		slog.Error("daemon", slog.String("error", "unkown arg size"), slog.String("name", cont.Name), slog.String("args", strings.Join(cont.HostArgs, " ")))
		return
	}

	err = cruntime.Run(cont)
	if err != nil {
		slog.Error("recover", slog.Any("error", err))
	}
	slog.Info("recover", slog.String("cont", cont.Name))

}
