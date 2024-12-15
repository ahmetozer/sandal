package cruntime

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/ahmetozer/sandal/pkg/controller"
)

func Kill(name string, signal int, second int) error {

	slog.Debug("Kill", slog.String("name", name), slog.Any("signal", signal), slog.Any("second", second))
	if c, err := controller.GetContainer(name); err == nil {
		if c.ContPid == 0 {
			c.Status = "killed"
			controller.SetContainer(c)
			return nil
		}
		ch := make(chan bool, 1)
		kill := make(chan bool)

		go func(killed chan<- bool) {
			// SendSig(c.HostPid, 9)
			SendSig(c.ContPid, signal)
			for {
				b, _ := IsPidRunning(c.ContPid)
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
			// Wait until exits
			ch <- <-kill
		}

		stat := <-ch

		if !stat {
			if second >= 0 {
				return fmt.Errorf("unable to kill container pid %d in %d second", c.ContPid, second)
			}
			return fmt.Errorf("unable to kill container pid: %d", c.ContPid)

		}

		c.Status = "killed"

		controller.SetContainer(c)

		return nil
	} else {
		slog.Debug("kill", slog.Any("cont", name), slog.Any("error", err))
		return err
	}

}
