package cmd

import (
	"flag"
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/cruntime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

func Stop(args []string) error {

	flags := flag.NewFlagSet("stop", flag.ExitOnError)
	var (
		help    bool
		signal  int
		timeout int
	)
	flags.BoolVar(&help, "help", false, "show this help message")
	flags.IntVar(&signal, "signal", 15, "default term signal")
	flags.IntVar(&timeout, "timeout", 30, "timeout to wait proccess")

	flags.Parse(args)

	if help {
		flags.Usage()
		return nil
	}

	leftArgs := flags.Args()
	if len(leftArgs) < 1 {
		return fmt.Errorf("no container name is provided")
	}

	cont, err := controller.GetContainer(leftArgs[0])
	if err != nil {
		return err
	}

	err = cruntime.Kill(cont, signal, timeout)
	if err != nil {
		return err
	}

	cont.Status = "stop"
	return controller.SetContainer(cont)
}
