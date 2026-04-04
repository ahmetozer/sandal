//go:build linux || darwin

package cmd

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/sandal"
	"github.com/ahmetozer/sandal/pkg/vm/terminal"
)

func ExecOnContainer(args []string) error {
	thisFlags, childArgs, splitFlagErr := sandal.SplitFlagsArgs(args)

	f := flag.NewFlagSet("exec", flag.ExitOnError)

	var (
		help bool
		Dir  string
		User string
		TTY  bool
	)

	f.BoolVar(&help, "help", false, "show this help message")
	f.StringVar(&Dir, "dir", "", "working directory")
	f.StringVar(&User, "user", "", "work user")
	f.BoolVar(&TTY, "t", false, "allocate a pseudo-TTY (for interactive shells)")

	if err := f.Parse(thisFlags); err != nil {
		return fmt.Errorf("error parsing flags: %v", err)
	}

	if help {
		f.Usage()
		return nil
	}

	if splitFlagErr != nil {
		return splitFlagErr
	}

	switch len(f.Args()) {
	case 0:
		return fmt.Errorf("please provide container name")
	case 1:
	default:
		return fmt.Errorf("multiple names provided, please provide only one: %v", f.Args())
	}

	contName := f.Args()[0]

	c, err := controller.GetContainer(contName)
	if err != nil {
		return fmt.Errorf("container %q not found: %w", contName, err)
	}

	// Auto-detect TTY: if stdin is a terminal and -t wasn't explicitly set,
	// enable TTY mode automatically for interactive commands.
	if !TTY {
		if fi, err := os.Stdin.Stat(); err == nil {
			TTY = (fi.Mode() & os.ModeCharDevice) != 0
		}
	}

	// Terminal raw mode + signal handling (CLI concerns)
	restore, rawErr := terminal.SetRaw()
	if rawErr != nil {
		restore = func() {}
	}
	defer restore()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	return sandal.Exec(c, childArgs, User, Dir, TTY, os.Stdin, os.Stdout, os.Stderr)
}
