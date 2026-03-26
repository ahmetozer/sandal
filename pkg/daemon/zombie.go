//go:build linux

package daemon

import (
	"log/slog"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/host"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

// checkZombie sends SIGTERM then SIGKILL to any startup containers still
// running during daemon shutdown. It gives processes up to 10 seconds to
// exit gracefully before force-killing, and gives up after 30 seconds to
// avoid blocking shutdown indefinitely (e.g. uninterruptible sleep).
func checkZombie() {
	const (
		gracePeriod  = 10 * time.Second
		maxWait      = 30 * time.Second
		pollInterval = 3 * time.Second
	)

	start := time.Now()
	deadline := start.Add(maxWait)
	graceExpiry := start.Add(gracePeriod)
	termSent := false

	for time.Now().Before(deadline) {
		conts, err := controller.Containers()
		if err != nil {
			slog.Warn("checkZombie", slog.String("action", "list containers"), slog.Any("error", err))
			time.Sleep(time.Second)
			continue
		}

		alive := false
		for _, cont := range conts {
			isRunning, err := crt.IsPidRunning(cont.ContPid)
			if !cont.Startup || !isRunning || err != nil {
				continue
			}
			alive = true

			if !termSent {
				slog.Info("checkZombie", slog.String("action", "SIGTERM"), slog.String("cont", cont.Name), slog.Int("pid", cont.ContPid))
				host.Kill(cont, 15, 0)
			} else if time.Now().After(graceExpiry) {
				slog.Warn("checkZombie", slog.String("action", "SIGKILL"), slog.String("cont", cont.Name), slog.Int("pid", cont.ContPid))
				host.Kill(cont, 9, 0)
			}
		}

		if !alive {
			slog.Debug("checkZombie", slog.String("status", "all containers stopped"))
			return
		}

		termSent = true
		time.Sleep(pollInterval)
	}

	slog.Warn("checkZombie", slog.String("status", "deadline reached, some containers may still be running"))
}
