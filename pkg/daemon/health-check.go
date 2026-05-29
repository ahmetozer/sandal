//go:build linux

package daemon

import (
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/host"
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
			conts, err := controller.Containers()
			if err != nil {
				slog.Warn("unable to get containers", "err", err.Error())
			}
			for c := range conts {
				cont := (conts)[c]

				// For VM containers, monitor HostPid (the KVM process);
				// for regular containers, monitor ContPid.
				checkPid := cont.ContPid
				if cont.VM != "" {
					checkPid = cont.HostPid
				}
				isRunning, err := crt.IsPidRunning(checkPid)
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
				pid := c.ContPid
				if c.VM != "" {
					pid = c.HostPid
				}
				if pid == 0 {
					return false
				}
				alive, _ := crt.IsPidRunning(pid)
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
var recovering sync.Map // map[string]time.Time

// maxRecoveryDuration bounds how long an in-flight claim is honored. A
// goroutine wedged in an uninterruptible syscall (e.g. a stalled squashfs
// mount) never runs its deferred release, so without a deadline a single
// hang would stop the container from ever recovering again. Past the
// deadline the claim is treated as stale and a fresh attempt is allowed.
//
// 90s assumes a recovery completes quickly, which holds for local-image
// (-lw squashfs/disk) containers. A container whose -lw is a registry ref
// can pull for up to the image-pull timeout (10m, see resolveLowerSource in
// pkg/container/host/rootfs.go); such a recovery may cross this deadline and
// get a second attempt stacked on it. Raise this above the pull timeout if
// registry-backed startup containers are used.
const maxRecoveryDuration = 90 * time.Second

// dispatchRecovery starts contRecover for cont unless a recovery is already
// in flight and still within maxRecoveryDuration. Only the (single-threaded)
// health-check loop calls this, so the Load/Store pair needs no extra
// locking; recovery goroutines only ever CompareAndDelete their own claim.
func dispatchRecovery(cont *config.Config) {
	now := time.Now()
	if v, busy := recovering.Load(cont.Name); busy {
		if now.Sub(v.(time.Time)) < maxRecoveryDuration {
			slog.Debug("daemon", slog.String("cont", cont.Name), slog.String("msg", "recovery in flight, skipping"))
			return
		}
		slog.Warn("daemon", slog.String("cont", cont.Name), slog.String("msg", "previous recovery exceeded deadline (likely wedged in a mount/network syscall); forcing a fresh attempt"), slog.String("age", now.Sub(v.(time.Time)).String()), slog.String("deadline", maxRecoveryDuration.String()))
	}
	token := now
	recovering.Store(cont.Name, token)
	go func() {
		// CompareAndDelete (not Delete) so a zombie goroutine that finally
		// unblocks after the deadline can't clear a newer recovery's claim.
		defer recovering.CompareAndDelete(cont.Name, token)
		contRecover(cont)
	}()
}

func contRecover(cont *config.Config) {
	if cont.Status == "stop" {
		return
	}
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
	isAlive, _ := crt.IsPidRunning(cont.ContPid)
	if isAlive {
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

	// If the container is already running (started by another path),
	// skip recovery to avoid duplicate instances.
	if isRunning, _ := crt.IsPidRunning(latest.ContPid); isRunning {
		slog.Debug("daemon", slog.String("cont", cont.Name), slog.String("msg", "already running, skipping recovery"))
		return
	}

	if len(latest.HostArgs) < 2 {
		slog.Error("daemon", slog.String("error", "unkown arg size"), slog.String("name", latest.Name), slog.String("args", strings.Join(latest.HostArgs, " ")))
		return
	}

	if latest.VM != "" {
		// VM container: re-run the full sandal.Run() pipeline which goes
		// through RunInKVM() again (re-pulls images, re-allocates network,
		// re-builds initrd, boots KVM).
		err = sandal.Run(latest.HostArgs[2:])
		if err != nil {
			slog.Error("recover vm", slog.String("cont", latest.Name), slog.Any("error", err))
		}
		slog.Info("recover", slog.String("cont", latest.Name), slog.String("type", "vm"))
		return
	}

	err = host.Run(latest)
	if err != nil {
		slog.Error("recover", slog.Any("error", err))
	}
	slog.Info("recover", slog.String("cont", latest.Name))

}
