//go:build linux || darwin

package cmd

import (
	"flag"
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/host"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

func Stop(args []string) error {

	flags := flag.NewFlagSet("stop", flag.ExitOnError)
	var (
		help    bool
		all     bool
		signal  int
		timeout int
	)
	flags.BoolVar(&help, "help", false, "show this help message")
	flags.BoolVar(&all, "all", false, "stop all running containers")
	flags.IntVar(&signal, "signal", 15, "default term signal")
	flags.IntVar(&timeout, "timeout", 30, "timeout to wait process")

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
		if err := stopContainer(name, signal, timeout); err != nil {
			fmt.Printf("stop %s: %s\n", name, err)
			lastErr = err
		}
	}
	return lastErr
}

func stopContainer(name string, signal, timeout int) error {
	cont, err := controller.GetContainer(name)
	if err != nil {
		return err
	}

	err = host.Kill(cont, signal, timeout)
	if err != nil {
		// Graceful signal timed out, escalate to SIGKILL
		if err2 := host.Kill(cont, 9, 5); err2 != nil {
			return fmt.Errorf("SIGTERM timed out and SIGKILL failed: %w", err2)
		}
	}

	cont.Status = "stop"
	return controller.SetContainer(cont)
}
