package cmd

import (
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

	conts, _ := config.AllContainers()
	for _, c := range conts {
		if c.Name == args[0] {
			err := container.CheckExistence(&c)
			if err != nil {
				return fmt.Errorf("unable to check existence of '%s' container: %v", c.Name, err)
			}
			if c.Status == container.ContainerStatusRunning {
				return fmt.Errorf("container %s is running, please stop it first", c.Name)
			}

			c.Keep = false
			deRunContainer(&c)

		}
	}
	return fmt.Errorf("container %s is not found", args[0])
}
