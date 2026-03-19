//go:build linux

package console

import (
	"log/slog"
	"os"
	"os/exec"
	"syscall"
)

// SetupSocketConsole sets up a PTY-based console served over a Unix socket (daemon mode).
// allocPTY and setPTYSize are passed from the caller to avoid cross-package dependency.
func SetupSocketConsole(
	name string,
	cmd *exec.Cmd,
	ptmx, ptySlave **os.File,
	consoleCleanup *func(),
	allocPTY func() (*os.File, *os.File, error),
	setPTYSize func(*os.File, uint16, uint16),
) {
	master, slave, ptyErr := allocPTY()
	if ptyErr != nil {
		slog.Warn("PTY allocation failed for background container", "error", ptyErr)
		return
	}

	setPTYSize(master, 24, 80)
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	var cloneflags uintptr
	if cmd.SysProcAttr != nil {
		cloneflags = cmd.SysProcAttr.Cloneflags
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: cloneflags,
		Setsid:     true,
		Setctty:    true,
		Ctty:       0,
	}
	*ptmx = master
	*ptySlave = slave

	cleanup, sockErr := StartSocket(name, master)
	if sockErr != nil {
		slog.Warn("console socket failed, falling back", "error", sockErr)
	} else {
		*consoleCleanup = cleanup
	}
}

// SetupFIFOConsole sets up FIFO/file-based console for daemonless background containers.
func SetupFIFOConsole(
	name string,
	cmd *exec.Cmd,
	consoleCleanup *func(),
) {
	stdin, stdout, stderr, cleanup, fifoErr := SetupFIFO(name)
	if fifoErr != nil {
		slog.Warn("console FIFO setup failed", "error", fifoErr)
		return
	}

	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	*consoleCleanup = cleanup
}
