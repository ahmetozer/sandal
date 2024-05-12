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
			ownerStatus := syscall.Kill(c.HostPid, syscall.Signal(0))
			if ownerStatus != nil || c.Background {
				deRunContainer(&c)
			}
			return err
		}
	}
	return fmt.Errorf("container %s is not found", args[0])
}
