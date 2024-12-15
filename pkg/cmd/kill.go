package cmd

import (
	"flag"
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/cruntime"
)

func Kill(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no container name is provided")
	}

	thisFlags, args := SplitFlagsArgs(args)
	flags := flag.NewFlagSet("kill", flag.ExitOnError)
	var (
		help    bool
		signal  int
		timeout int
	)
	flags.BoolVar(&help, "help", false, "show this help message")
	flags.IntVar(&signal, "signal", 9, "default kill signal")
	flags.IntVar(&timeout, "timeout", 5, "timeout to wait proccess")

	flags.Parse(thisFlags)

	if help {
		flags.Usage()
		return nil
	}

	return cruntime.Kill(args[0], 9, timeout)
}
