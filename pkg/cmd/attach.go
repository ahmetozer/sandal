//go:build linux

package cmd

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/sandal"
	"golang.org/x/sys/unix"
)

func Attach(args []string) error {
	flags := flag.NewFlagSet("attach", flag.ExitOnError)
	var help bool

	flags.BoolVar(&help, "help", false, "show this help message")
	flags.Parse(args)

	if help || len(flags.Args()) < 1 {
		fmt.Printf("Usage: %s attach CONTAINER\n\nAttach to a running background container's console.\n\nDetach with Ctrl+P, Ctrl+Q (socket mode).\n\nOPTIONS:\n", os.Args[0])
		flags.PrintDefaults()
		return nil
	}

	containerName := flags.Args()[0]

	c, err := controller.GetContainer(containerName)
	if err != nil {
		return fmt.Errorf("container %q not found: %w", containerName, err)
	}

	// For VM containers, set up terminal and delegate
	if c.VM != "" {
		restore := setupRawTerminal()
		defer restore()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
		defer signal.Stop(sigCh)

		fmt.Fprintf(os.Stderr, "Attached to %s (Ctrl+C to detach)\r\n", containerName)
		err := sandal.Attach(c, os.Stdin, os.Stdout, os.Stderr, nil)
		fmt.Fprintf(os.Stderr, "\nDetached from %s\n", containerName)
		return err
	}

	// Native container — verify running
	running, _ := crt.IsPidRunning(c.ContPid)
	if !running {
		return fmt.Errorf("container %q is not running", containerName)
	}

	// Create done channel that closes when container exits
	done := make(chan struct{})
	go func() {
		for {
			if running, _ := crt.IsPidRunning(c.ContPid); !running {
				close(done)
				return
			}
			unix.Nanosleep(&unix.Timespec{Sec: 1}, nil)
		}
	}()

	// Set up terminal raw mode
	restore := setupRawTerminal()
	defer restore()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer signal.Stop(sigCh)

	fmt.Fprintf(os.Stderr, "Attached to %s (Ctrl+P, Ctrl+Q to detach)\r\n", containerName)
	err = sandal.Attach(c, os.Stdin, os.Stdout, os.Stderr, done)
	fmt.Fprintf(os.Stderr, "\nDetached from %s\n", containerName)
	return err
}

// setupRawTerminal puts the terminal into raw mode and returns a restore function.
func setupRawTerminal() func() {
	oldTermios, err := makeRaw(os.Stdin)
	if err != nil {
		return func() {}
	}
	// restoreTerminal restores the terminal to its previous state.
	restoreTerminal := func(f *os.File, termios *unix.Termios) {
		unix.IoctlSetTermios(int(f.Fd()), unix.TCSETS, termios)
	}

	return func() { restoreTerminal(os.Stdin, oldTermios) }
}

// makeRaw puts the terminal into raw mode and returns the previous state.
func makeRaw(f *os.File) (*unix.Termios, error) {
	fd := int(f.Fd())
	oldTermios, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}

	raw := *oldTermios
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0

	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &raw); err != nil {
		return nil, err
	}
	return oldTermios, nil
}
