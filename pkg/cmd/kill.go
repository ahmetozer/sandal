//go:build linux || darwin

package cmd

import (
	"flag"
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/host"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

func Kill(args []string) error {

	flags := flag.NewFlagSet("kill", flag.ExitOnError)
	var (
		help    bool
		all     bool
		signal  int
		timeout int
	)
	flags.BoolVar(&help, "help", false, "show this help message")
	flags.BoolVar(&all, "all", false, "kill all running containers")
	flags.IntVar(&signal, "signal", 9, "default kill signal")
	flags.IntVar(&timeout, "timeout", 5, "timeout to wait process")

	flags.Parse(args)

	if help {
		flags.Usage()
		return nil
	}

	names := flags.Args()

	if all {
		conts, err := controller.Containers()
		if err != nil {
			return fmt.Errorf("unable to list containers: %w", err)
		}
		for _, cont := range conts {
			isRunning, _ := crt.IsPidRunning(cont.ContPid)
			if isRunning {
				names = append(names, cont.Name)
			}
		}
	}

	if len(names) < 1 {
		return fmt.Errorf("no container name is provided")
	}

	var lastErr error
	for _, name := range names {
		if err := host.KillByName(name, signal, timeout); err != nil {
			fmt.Printf("kill %s: %s\n", name, err)
			lastErr = err
		}
	}
	return lastErr
}
