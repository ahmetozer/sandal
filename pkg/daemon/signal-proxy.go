package daemon

import (
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/container/cruntime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

func signalProxy(daemonKillRequested chan<- bool, wg *sync.WaitGroup) {
	done := make(chan os.Signal, 1)
	slog.Info("signalProxy", "service", "started")
	defer wg.Done()
	for {
		signal.Notify(done, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
		sig := <-done
		conts, _ := controller.Containers()
		for _, cont := range conts {
			// oldContPid := cont.ContPid
			if cont.Startup && cruntime.IsRunning(cont) {
				cruntime.Kill(cont.Name, int(sig.(syscall.Signal)), 0)
			}
		}
		// syscall.Kill(os.Getpid(), sig.(syscall.Signal))
		if sig == syscall.SIGTERM || sig == syscall.SIGINT || sig == syscall.SIGKILL || sig == syscall.SIGQUIT {
			slog.Info("signalProxy", slog.String("daemonKill", sig.String()))
			daemonKillRequested <- true
			break
		}
	}
	slog.Info("signalProxy", "service", "stopped")
}
