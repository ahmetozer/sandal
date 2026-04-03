//go:build linux || darwin

package cmd

import (
	"flag"
	"fmt"
	"log/slog"

	"github.com/ahmetozer/sandal/pkg/container/host"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

func Clear(args []string) error {

	f := flag.NewFlagSet("exec", flag.ExitOnError)

	var (
		help      bool
		deleteAll bool
	)

	f.BoolVar(&help, "help", false, "show this help message")
	f.BoolVar(&deleteAll, "all", false, "delete all containers which they are not in running state")

	if err := f.Parse(args); err != nil {
		return fmt.Errorf("error parsing flags: %v", err)
	}

	if help {
		f.Usage()
		return nil
	}

	conts, _ := controller.Containers()
	for _, c := range conts {
		if !deleteAll {
			if !c.Remove {
				continue
			}
		}
		isRunning, err := crt.IsPidRunning(c.ContPid)

		if err != nil {
			slog.Error("unable to get container status", "container", c.Name, "err", err)
		}
		if isRunning {
			slog.Warn("container is running", "container", c.Name, "rm", c.Remove)
			continue
		}
		if deleteAll {
			c.Remove = true
		}
		host.DeRunContainer(c)
	}
	return nil
}
