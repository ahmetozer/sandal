package cmd

import (
	"flag"
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/cruntime"
)

func Kill(args []string) error {

	flags := flag.NewFlagSet("kill", flag.ExitOnError)
	var (
		help    bool
		signal  int
		timeout int
	)
	flags.BoolVar(&help, "help", false, "show this help message")
	flags.IntVar(&signal, "signal", 9, "default kill signal")
	flags.IntVar(&timeout, "timeout", 5, "timeout to wait proccess")

	flags.Parse(args)

	if help {
		flags.Usage()
		return nil
	}

	leftArgs := flags.Args()
	if len(leftArgs) < 1 {
		return fmt.Errorf("no container name is provided")
	}

	return cruntime.KillByName(leftArgs[0], signal, timeout)
}
