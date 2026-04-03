//go:build linux || darwin

package cmd

import (
	"errors"
	"flag"
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/host"
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
			isRunning, _ := crt.IsPidRunning(c.ContPid)
			if !isRunning {
				names = append(names, c.Name)
			}
		}
	}

	if len(names) < 1 {
		return fmt.Errorf("no container name is provided")
	}

	var errs []error
RequestedContainers:
	for _, name := range names {
		for _, c := range conts {
			if c.Name == name {
				isRunning, err := crt.IsPidRunning(c.ContPid)

				if err != nil {
					errs = append(errs, fmt.Errorf("unable to check existence of '%s' container: %v", c.Name, err))
				}
				if isRunning {
					errs = append(errs, fmt.Errorf("container %s is running, please stop it first", c.Name))
					continue RequestedContainers
				}

				c.Remove = true
				host.DeRunContainer(c)
				continue RequestedContainers
			}
		}
		errs = append(errs, fmt.Errorf("container %s is not found", name))
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
