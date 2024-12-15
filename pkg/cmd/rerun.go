package cmd

import (
	"fmt"
	"os"

	"github.com/ahmetozer/sandal/pkg/container/cruntime"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
	"golang.org/x/sys/unix"
)

func Rerun(args []string) error {
	if len(args) == 0 || args[0] == "" {
		return fmt.Errorf("no container name provided")
	} else if args[0] == "help" {
		fmt.Printf("%s rerun ${container id}\n", os.Args[0])
		exitCode = 0
		return nil
	}

	c, err := controller.GetContainer(args[0])
	if err != nil {
		return err
	}

	err = cruntime.Kill(args[0], 9, 5)
	if err != nil {
		return err
	}

	err = unix.Exec(env.BinLoc, c.HostArgs, os.Environ())
	if err != nil {
		return fmt.Errorf("unable to rerun %s", err)
	}

	return nil

}
