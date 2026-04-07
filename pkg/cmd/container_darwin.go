//go:build darwin

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

func Daemon(args []string) error {
	return fmt.Errorf("daemon mode is not yet available on macOS")
}

// Attach on macOS: terminal setup + delegate to sandal.Attach.
func Attach(args []string) error {
	flags := flag.NewFlagSet("attach", flag.ExitOnError)
	var help bool
	flags.BoolVar(&help, "help", false, "show this help message")
	flags.Parse(args)

	if help || len(flags.Args()) < 1 {
		fmt.Printf("Usage: %s attach CONTAINER\n\nAttach to a running container's console.\n\nOPTIONS:\n", os.Args[0])
		flags.PrintDefaults()
		return nil
	}

	containerName := flags.Args()[0]
	c, err := controller.GetContainer(containerName)
	if err != nil {
		return fmt.Errorf("container %q not found: %w", containerName, err)
	}

	restore, rawErr := terminal.SetRaw()
	if rawErr != nil {
		restore = func() {}
	}
	defer restore()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer signal.Stop(sigCh)

	fmt.Fprintf(os.Stderr, "Attached to %s (Ctrl+C to detach)\r\n", containerName)
	err = sandal.Attach(c, os.Stdin, os.Stdout, os.Stderr, nil)
	fmt.Fprintf(os.Stderr, "\nDetached from %s\n", containerName)
	return err
}
