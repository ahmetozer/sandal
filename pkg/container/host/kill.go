//go:build linux

package host

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

func KillByName(name string, signal int, second int) error {

	slog.Debug("Kill", slog.String("name", name), slog.Any("signal", signal), slog.Any("second", second))
	if c, err := controller.GetContainer(name); err == nil {
		return Kill(c, signal, second)
	} else {
		slog.Debug("kill", slog.Any("cont", name), slog.Any("error", err))
		return err
	}

}

func Kill(c *config.Config, signal int, second int) error {
	if c.ContPid == 0 {
		if c.Status != "killed" {
			c.Status = "killed"
			return controller.SetContainer(c)
		}
		return nil
	}
	pid := c.ContPid

	// Identity guard: only signal if pid is still THIS container. If it's dead,
	// or recycled to an unrelated process (different start-time), signalling it
	// would kill an innocent process — treat it as already stopped and clean up.
	if alive, _ := crt.IsPidRunningAs(pid, c.ContPidStart); !alive {
		c.Status = "killed"
		c.ContPid = 0
		c.ContPidStart = 0
		CleanupResources(c)
		return controller.SetContainer(c)
	}

	// Deliver the signal once.
	crt.SendSig(pid, signal)

	// second == 0: fire-and-forget. Deliver the signal and return without
	// waiting or tearing down state. The signal proxy, zombie reaper, and
	// recovery's last-resort SIGKILL all pass 0 and poll for exit themselves;
	// they must not block here and must not trigger cleanup on a process that
	// may still be alive.
	if second == 0 {
		return nil
	}

	// Wait for the process to actually exit. second < 0 waits indefinitely;
	// second > 0 polls until the deadline. There is no background goroutine,
	// so nothing can leak; reading /proc never blocks (even for D-state pids),
	// so the caller is blocked for at most `second`.
	deadline := time.Now().Add(time.Duration(second) * time.Second)
	exited := false
	for {
		// Identity-aware so our process exiting (even if the pid is instantly
		// recycled by an unrelated process) counts as exited.
		if alive, _ := crt.IsPidRunningAs(pid, c.ContPidStart); !alive {
			exited = true
			break
		}
		if second > 0 && !time.Now().Before(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !exited {
		return fmt.Errorf("unable to kill container pid %d in %d second", pid, second)
	}

	c.Status = "killed"
	c.ContPid = 0
	c.ContPidStart = 0
	CleanupResources(c)
	controller.SetContainer(c)

	return nil
}
