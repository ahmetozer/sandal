//go:build linux || darwin

package cmd

import (
	"errors"
	"flag"
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/host"
	"github.com/ahmetozer/sandal/pkg/container/namelock"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

func Rm(args []string) error {
	flags := flag.NewFlagSet("rm", flag.ExitOnError)
	var (
		help bool
		all  bool
	)
	flags.BoolVar(&help, "help", false, "show this help message")
	flags.BoolVar(&all, "all", false, "remove all stopped containers")
	flags.Parse(args)

	if help {
		flags.Usage()
		return nil
	}

	conts, err := controller.Containers()
	if err != nil {
		return fmt.Errorf("unable to list containers: %w", err)
	}

	names := flags.Args()

	if all {
		for _, c := range conts {
			pid, wantStart := c.MonitorPidIdentity()
			isRunning, _ := crt.IsPidRunningAs(pid, wantStart)
			if !isRunning {
				names = append(names, c.Name)
			}
		}
	}

	if len(names) < 1 {
		return fmt.Errorf("no container name is provided")
	}

	var errs []error
	for _, name := range names {
		exists := false
		for _, c := range conts {
			if c.Name == name {
				exists = true
				break
			}
		}
		if !exists {
			errs = append(errs, fmt.Errorf("container %s is not found", name))
			continue
		}

		// Hold the per-name lifecycle lock and re-read the latest config under
		// it, so we never tear down (and delete the config/rootfs of) a
		// container the daemon just (re)started between our snapshot and now,
		// which would leave that fresh instance detached (audit L5).
		release, err := namelock.Acquire(name, namelock.DefaultTimeout)
		if err != nil {
			errs = append(errs, fmt.Errorf("rm %s: acquire lifecycle lock: %w", name, err))
			continue
		}

		c, err := controller.GetContainer(name)
		if err != nil {
			release()
			errs = append(errs, fmt.Errorf("container %s is not found", name))
			continue
		}

		pid, wantStart := c.MonitorPidIdentity()
		if isRunning, rerr := crt.IsPidRunningAs(pid, wantStart); rerr != nil {
			errs = append(errs, fmt.Errorf("unable to check existence of '%s' container: %v", name, rerr))
		} else if isRunning {
			release()
			errs = append(errs, fmt.Errorf("container %s is running, please stop it first", name))
			continue
		}

		c.Remove = true
		host.DeRunContainer(c)
		release()
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
