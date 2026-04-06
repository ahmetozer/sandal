//go:build linux || darwin

package cmd

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/sandal"
	"github.com/ahmetozer/sandal/pkg/vm/terminal"
)

// stringSliceFlag collects repeated -env-pass values.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string     { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error { *s = append(*s, v); return nil }

func ExecOnContainer(args []string) error {
	thisFlags, childArgs, splitFlagErr := sandal.SplitFlagsArgs(args)

	f := flag.NewFlagSet("exec", flag.ExitOnError)

	var (
		help    bool
		Dir     string
		User    string
		TTY     bool
		EnvAll  bool
		EnvPass stringSliceFlag
	)

	f.BoolVar(&help, "help", false, "show this help message")
	f.StringVar(&Dir, "dir", "", "working directory")
	f.StringVar(&User, "user", "", "work user")
	f.BoolVar(&TTY, "t", false, "allocate a pseudo-TTY (for interactive shells)")
	f.Var(&EnvPass, "env-pass", "pass a specific environment variable by name from the caller (repeatable)")
	f.BoolVar(&EnvAll, "env-all", false, "pass all environment variables from the caller")

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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	// Set terminal raw mode only for TTY sessions. Defer restore so the
	// terminal is always cleaned up, even if exec fails mid-stream.
	if TTY {
		restore, rawErr := terminal.SetRaw()
		if rawErr != nil {
			restore = func() {}
		}
		defer restore()
	}

	// Collect env vars to forward to the container command.
	var extraEnv []string
	if EnvAll {
		extraEnv = append(extraEnv, os.Environ()...)
	} else {
		for _, name := range EnvPass {
			if v, ok := os.LookupEnv(name); ok {
				extraEnv = append(extraEnv, name+"="+v)
			}
		}
	}

	return sandal.Exec(c, childArgs, User, Dir, TTY, extraEnv, os.Stdin, os.Stdout, os.Stderr)
}
