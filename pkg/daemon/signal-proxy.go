//go:build linux

package daemon

import (
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/sandal"
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
			// Monitor HostPid for VMs, ContPid for native — same rule and
			// identity check as the health-check.
			pid, wantStart := cont.MonitorPidIdentity()
			isRunning, err := crt.IsPidRunningAs(pid, wantStart)
			if cont.Startup && isRunning && err == nil {
				// sandal.Kill routes VM containers to killVMHost (HostPid);
				// host.Kill alone targets ContPid and never reaps a VM (L7).
				sandal.Kill(cont, int(sig.(syscall.Signal)), 0)
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
