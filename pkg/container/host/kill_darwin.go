//go:build darwin

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

// Kill sends a signal to the VM host process (HostPid) on macOS.
// Killing the host process terminates the VZ VM and the container within it.
func Kill(c *config.Config, signal int, second int) error {
	// On macOS, ContPid is inside the VM (not visible from host).
	// Use HostPid (the sandal process running the VZ VM).
	pid := c.HostPid
	if pid == 0 {
		if c.Status != "killed" {
			c.Status = "killed"
			return controller.SetContainer(c)
		}
		return nil
	}

	// Deliver the signal once.
	crt.SendSig(pid, signal)

	// second == 0: fire-and-forget. Deliver the signal and return without
	// waiting or tearing down state. Callers that pass 0 poll for exit
	// themselves and must not block here or trigger cleanup on a process
	// that may still be alive.
	if second == 0 {
		return nil
	}

	// Wait for the process to actually exit. second < 0 waits indefinitely;
	// second > 0 polls until the deadline. No background goroutine, so
	// nothing can leak; the caller is blocked for at most `second`.
	deadline := time.Now().Add(time.Duration(second) * time.Second)
	exited := false
	for {
		if alive, _ := crt.IsPidRunning(pid); !alive {
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
	c.HostPid = 0
	CleanupResources(c)
	controller.SetContainer(c)

	return nil
}
