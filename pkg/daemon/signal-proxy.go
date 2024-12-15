package daemon

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/container/cruntime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

func signalProxy(daemonKillRequested *bool) {
	done := make(chan os.Signal, 1)
	for {
		signal.Notify(done, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
		sig := <-done
		conts, _ := controller.Containers()
		for _, cont := range conts {
			// oldContPid := cont.ContPid
			if cont.Startup && cruntime.IsRunning(cont) {
				syscall.Kill(cont.ContPid, sig.(syscall.Signal))
			}
		}
		// syscall.Kill(os.Getpid(), sig.(syscall.Signal))
		if sig == syscall.SIGTERM || sig == syscall.SIGINT || sig == syscall.SIGKILL || sig == syscall.SIGQUIT {
			slog.Info("signalProxy", slog.String("daemonKill", sig.String()))
			*daemonKillRequested = true
			break
		}
	}
}
