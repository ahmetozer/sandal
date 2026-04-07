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

	ch := make(chan bool, 1)
	kill := make(chan bool)

	go func(killed chan<- bool) {
		crt.SendSig(pid, signal)
		for {
			b, _ := crt.IsPidRunning(pid)
			if !b {
				killed <- true
				break
			}
			time.Sleep(1 * time.Second)
		}
	}(kill)

	if second >= 0 {
		select {
		case ret := <-kill:
			ch <- ret
		case <-time.After(time.Duration(second) * time.Second):
			ch <- false
		}
	} else {
		ch <- <-kill
	}

	stat := <-ch

	if !stat {
		if second >= 0 {
			return fmt.Errorf("unable to kill container pid %d in %d second", pid, second)
		}
		return fmt.Errorf("unable to kill container pid: %d", pid)
	}

	c.Status = "killed"
	c.HostPid = 0
	CleanupResources(c)
	controller.SetContainer(c)

	return nil
}
