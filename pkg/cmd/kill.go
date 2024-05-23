package cmd

import (
	"flag"
	"fmt"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/config"
)

func kill(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no container name is provided")
	}

	thisFlags, args := SplitArgs(args)
	flags := flag.NewFlagSet("kill", flag.ExitOnError)
	var (
		help   bool
		signal int
	)
	flags.BoolVar(&help, "help", false, "show this help message")
	flags.IntVar(&signal, "signal", 9, "default kill signal")

	flags.Parse(thisFlags)

	config.AllContainers()
	for _, c := range config.AllContainers() {
		if c.Name == args[0] {
			err := syscall.Kill(c.ContPid, syscall.Signal(signal))
			err2 := syscall.Kill(c.HostPid, syscall.Signal(signal))
			deRunContainer(&c)

			if err == err2 && err2 == nil {
				return nil
			}
			if err2 != nil && err != nil {
				return fmt.Errorf("unable to kill '%s' container and container host: cont: %v, host: %v", c.Name, err, err2)
			}

			if err != nil {
				return fmt.Errorf("unable to kill '%s' container: %v", c.Name, err)
			}
			if err2 != nil {
				return fmt.Errorf("unable to kill '%s' container host: %v", c.Name, err2)
			}

			return err
		}
	}
	return fmt.Errorf("container %s is not found", args[0])
}
