//go:build linux

package daemon

import (
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/host"
	"github.com/ahmetozer/sandal/pkg/container/namelock"
	"github.com/ahmetozer/sandal/pkg/container/net/renumber"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/sandal"
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
			// Disk-authoritative reconcile first, so the per-container checks
			// below act on state that matches disk even if the inotify watcher
			// missed events (audit L6/L8).
			reconcileState()

			conts, err := controller.Containers()
			if err != nil {
				slog.Warn("unable to get containers", "err", err.Error())
			}
			for c := range conts {
				cont := (conts)[c]

				// Monitor HostPid for VMs (the KVM process), ContPid for
				// regular containers, and verify the start-time so a recycled
				// pid isn't mistaken for a still-running container.
				checkPid, wantStart := cont.MonitorPidIdentity()
				isRunning, err := crt.IsPidRunningAs(checkPid, wantStart)
				if err != nil {
					slog.Warn("unable to get container status", "cont", cont.Name, "err", err.Error())
				}
				slog.Debug("daemon", slog.String("action", "healthCheck"), slog.Any("len", len((conts))), slog.String("cont", cont.Name), slog.Bool("running", isRunning))
				if !isRunning {
					// Release any rehydrated forward session for this
					// container. contRecover takes care of cleanup for
					// containers it restarts (Forwards.Add replaces the
					// session), but if Startup=false there is no recover
					// path and the rehydrated listener would leak. Stop
					// the named entry; if the recover path then fires it
					// will install a fresh session.
					host.Forwards.Stop(cont.Name)
					if cont.Startup {
						dispatchRecovery(cont)
					} else {
						slog.Debug("daemon", slog.String("cont", cont.Name), slog.String("msg", "recovering bypassed"))
					}
				}
			}
			// After per-container checks, reconcile NDP proxy entries on
			// the upstream interface (no-op when dynamic IPv6 is not
			// configured or proxy is nil). Pass an isAlive callback that
			// consults the kernel PID so post-crash stale "running"
			// statuses and "killed"-status containers don't keep proxy
			// entries pinned.
			renumber.ReconcileProxyForRunning(conts, func(c *config.Config) bool {
				pid, wantStart := c.MonitorPidIdentity()
				if pid == 0 {
					return false
				}
				alive, _ := crt.IsPidRunningAs(pid, wantStart)
				return alive
			})
		}
	}

}

// recovering tracks containers with an in-flight contRecover, keyed by
// name with the time the recovery was claimed. It serializes recovery per
// container: without it the 3-second health-check tick stacks concurrent
// restarts when a recovery outlasts one tick, and each host.Run grabs a
// fresh loop device, leaking the previous run's immutable mount.
// recovering holds names with an in-flight recovery goroutine, so the 3-second
// health-check tick doesn't stack duplicate goroutines for the same container
// while a recovery is still running. Cross-process and cross-goroutine
// exclusion (against CLI run/kill/rm and against another recovery) is enforced
// by the per-name lifecycle lock that contRecover takes; this set only avoids
// redundant local goroutines.
var recovering sync.Map // map[string]struct{}

// recoveryWG tracks in-flight recovery goroutines so the shutdown path can
// drain them before tearing containers down — otherwise a recovery that is
// mid-placement when the daemon exits orphans a freshly started child (L1).
var recoveryWG sync.WaitGroup

// shuttingDown, once set by the shutdown path, makes dispatchRecovery refuse to
// launch new recoveries while the daemon is winding down.
var shuttingDown atomic.Bool

// beginShutdown stops new recoveries and blocks until in-flight ones finish.
// Called from the daemon shutdown path before containers are torn down.
func beginShutdown() {
	shuttingDown.Store(true)
	recoveryWG.Wait()
}

// dispatchRecovery starts contRecover for cont unless a recovery is already in
// flight for that name or the daemon is shutting down. Only the
// (single-threaded) health-check loop calls this.
func dispatchRecovery(cont *config.Config) {
	if shuttingDown.Load() {
		return
	}
	if _, busy := recovering.LoadOrStore(cont.Name, struct{}{}); busy {
		slog.Debug("daemon", slog.String("cont", cont.Name), slog.String("msg", "recovery in flight, skipping"))
		return
	}
	recoveryWG.Add(1)
	go func() {
		defer recoveryWG.Done()
		defer recovering.Delete(cont.Name)
		contRecover(cont)
	}()
}

func contRecover(cont *config.Config) {
	if cont.Status == "stop" {
		return
	}

	// Take the cross-process per-name lifecycle lock so this recovery cannot run
	// concurrently with a CLI run/kill/rm for the same name, nor with another
	// recovery. This is held across the kill+placement and replaces the old
	// wall-clock deadline that could stack a second recovery and double-start
	// the container (audit L2). Non-blocking: if another actor owns the name,
	// skip this round; the next tick retries once they release.
	release, ok, lerr := namelock.TryAcquire(cont.Name)
	if lerr != nil {
		slog.Warn("daemon", slog.String("cont", cont.Name), slog.String("msg", "lifecycle lock error"), slog.Any("err", lerr))
		return
	}
	if !ok {
		slog.Debug("daemon", slog.String("cont", cont.Name), slog.String("msg", "name busy, deferring recovery to next tick"))
		return
	}
	defer release() // idempotent; the VM/native paths may release earlier

	slog.Debug("daemon", slog.Any("action", "killing old"), slog.String("cont", cont.Name), slog.Any("contpid", cont.ContPid), slog.Any("hostpid", cont.HostPid))

	// Clean up stale resources (console sockets, mounts, cgroups) left
	// behind by a crashed daemon (e.g. kill -9). The old process is dead
	// so these resources are orphaned.
	host.CleanupResources(cont)

	err := host.Kill(cont, 15, 10)
	if err != nil {
		host.Kill(cont, 9, 0)
	}

	// After a daemon crash (kill -9), the config status on disk is still
	// "running" because it was never updated. Since the health check
	// already confirmed the PID is dead, treat any non-"stop" status
	// as recoverable.
	contPid, contStart := cont.MonitorPidIdentity()
	if isAlive, _ := crt.IsPidRunningAs(contPid, contStart); isAlive {
		slog.Debug("daemon", slog.Any("status", cont.Status), slog.String("cont", cont.Name), slog.String("msg", "process still alive, skipping recovery"))
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

	// Re-check under the lock: the user may have stopped it (status "stop") or
	// it may have been started by another path since detection.
	if latest.Status == "stop" {
		slog.Debug("daemon", slog.String("cont", cont.Name), slog.String("msg", "status stop, skipping recovery"))
		return
	}
	latestPid, latestStart := latest.MonitorPidIdentity()
	if isRunning, _ := crt.IsPidRunningAs(latestPid, latestStart); isRunning {
		slog.Debug("daemon", slog.String("cont", cont.Name), slog.String("msg", "already running, skipping recovery"))
		return
	}

	if len(latest.HostArgs) < 2 {
		slog.Error("daemon", slog.String("error", "unkown arg size"), slog.String("name", latest.Name), slog.String("args", strings.Join(latest.HostArgs, " ")))
		return
	}

	if latest.VM != "" {
		// VM recovery re-runs the full sandal.Run() pipeline, which goes through
		// RunInKVM() and takes the per-name lock ITSELF. Release ours first to
		// avoid a self-deadlock (flock contends even within one process).
		release()
		if err = sandal.Run(latest.HostArgs[2:]); err != nil {
			slog.Error("recover vm", slog.String("cont", latest.Name), slog.Any("error", err))
		}
		slog.Info("recover", slog.String("cont", latest.Name), slog.String("type", "vm"))
		return
	}

	// Native recovery: pass release as onPlaced so the lifecycle lock drops as
	// soon as the recovered child's pid is published.
	if err = host.Run(latest, release); err != nil {
		slog.Error("recover", slog.Any("error", err))
	}
	slog.Info("recover", slog.String("cont", latest.Name))
}
