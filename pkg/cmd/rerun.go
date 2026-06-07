//go:build linux || darwin

package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/ahmetozer/sandal/pkg/container/namelock"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/sandal"
)

func Rerun(args []string) error {
	if len(args) == 0 || args[0] == "" {
		return fmt.Errorf("no container name provided")
	} else if args[0] == "help" {
		fmt.Printf("%s rerun ${container id}\n", os.Args[0])
		exitCode = 0
		return nil
	}

	c, err := controller.GetContainer(args[0])
	if err != nil {
		return err
	}

	// Hold the per-name lifecycle lock across the kill so it can't race a
	// concurrent run/recover, then release before Run (which re-acquires the
	// same lock). sandal.Kill routes VM containers to their HostPid.
	release, err := namelock.Acquire(c.Name, namelock.DefaultTimeout)
	if err != nil {
		return fmt.Errorf("acquire lifecycle lock: %w", err)
	}
	err = sandal.Kill(c, 9, 5)
	release()
	if err != nil {
		return err
	}

	if len(c.HostArgs) < 2 {
		return fmt.Errorf("not enough argment len(arg)< 2 %v", c.HostArgs)
	}

	slog.Debug("Rerun", slog.String("message", "re-executing command"), slog.Any("args", c.HostArgs[2:]))
	return Run(c.HostArgs[2:])

}
