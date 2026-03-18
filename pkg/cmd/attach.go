//go:build linux

package cmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/ahmetozer/sandal/pkg/container/cruntime"
	"github.com/ahmetozer/sandal/pkg/container/cruntime/console"
	"github.com/ahmetozer/sandal/pkg/controller"
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

	// Verify container is running
	running, _ := cruntime.IsPidRunning(c.ContPid)
	if !running {
		return fmt.Errorf("container %q is not running", containerName)
	}

	// Detect console mode from the mode file
	modeBytes, err := os.ReadFile(console.ModePath(containerName))
	if err != nil {
		return fmt.Errorf("no console available for %q (was it started in background?)", containerName)
	}
	mode := string(modeBytes)

	// Create a done channel that closes when the container exits
	done := make(chan struct{})
	go func() {
		for {
			if running, _ := cruntime.IsPidRunning(c.ContPid); !running {
				close(done)
				return
			}
			unix.Nanosleep(&unix.Timespec{Sec: 1}, nil)
		}
	}()

	switch mode {
	case console.ModeSocket:
		// Put terminal in raw mode for full PTY experience
		oldTermios, rawErr := makeRaw(os.Stdin)
		if rawErr == nil {
			defer restoreTerminal(os.Stdin, oldTermios)
		}

		fmt.Fprintf(os.Stderr, "Attached to %s (Ctrl+P, Ctrl+Q to detach)\r\n", containerName)
		err = console.AttachSocket(containerName, os.Stdin, os.Stdout, done)

	case console.ModeFIFO:
		fmt.Fprintf(os.Stderr, "Attached to %s (Ctrl+C to detach)\n", containerName)
		err = console.AttachFIFO(containerName, os.Stdin, os.Stdout, os.Stderr, done)

	default:
		return fmt.Errorf("unknown console mode: %s", mode)
	}

	fmt.Fprintf(os.Stderr, "\nDetached from %s\n", containerName)
	return err
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

// restoreTerminal restores the terminal to its previous state.
func restoreTerminal(f *os.File, termios *unix.Termios) {
	unix.IoctlSetTermios(int(f.Fd()), unix.TCSETS, termios)
}
