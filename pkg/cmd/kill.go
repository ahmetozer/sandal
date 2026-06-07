//go:build linux || darwin

package cmd

import (
	"flag"
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/namelock"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/sandal"
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
			pid := cont.ContPid
			if pid == 0 && cont.VM != "" {
				pid = cont.HostPid
			}
			if pid == 0 {
				continue
			}
			isRunning, _ := crt.IsPidRunning(pid)
			if isRunning {
				names = append(names, cont.Name)
			}
		}
		if len(names) < 1 {
			return nil
		}
	}

	if len(names) < 1 {
		return fmt.Errorf("no container name is provided")
	}

	var lastErr error
	for _, name := range names {
		// Hold the per-name lifecycle lock so a kill can't race a concurrent
		// daemon recovery / run of the same name (which would kill the wrong
		// instance or leave one detached).
		release, err := namelock.Acquire(name, namelock.DefaultTimeout)
		if err != nil {
			fmt.Printf("kill %s: %s\n", name, err)
			lastErr = err
			continue
		}
		c, err := controller.GetContainer(name)
		if err != nil {
			release()
			fmt.Printf("kill %s: %s\n", name, err)
			lastErr = err
			continue
		}
		if err := sandal.Kill(c, signal, timeout); err != nil {
			fmt.Printf("kill %s: %s\n", name, err)
			lastErr = err
		}
		release()
	}
	return lastErr
}
