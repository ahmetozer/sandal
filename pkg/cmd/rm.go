package cmd

import (
	"errors"
	"flag"
	"fmt"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/container"
)

func rm(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no container name is provided")
	}

	thisFlags, args := SplitFlagsArgs(args)
	flags := flag.NewFlagSet("clear", flag.ExitOnError)
	var (
		help bool
	)
	flags.BoolVar(&help, "help", false, "show this help message")
	flags.Parse(thisFlags)

	conts, _ := config.Containers()
	var errs []error
RequestedContainers:
	for _, name := range args {
		for _, c := range conts {
			if c.Name == name {
				err := container.CheckExistence(&c)
				if err != nil {
					errs = append(errs, fmt.Errorf("unable to check existence of '%s' container: %v", c.Name, err))
				}
				if c.Status == container.ContainerStatusRunning {
					errs = append(errs, fmt.Errorf("container %s is running, please stop it first", c.Name))
				}

				c.Remove = true
				deRunContainer(&c)
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
