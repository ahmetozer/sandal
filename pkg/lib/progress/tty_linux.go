//go:build linux

package progress

import (
	"os"

	"golang.org/x/sys/unix"
)

func isTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	return err == nil
}

func terminalWidth(f *os.File) int {
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 {
		return 80
	}
	return int(ws.Col)
}

var savedTermios *unix.Termios

// disableEcho turns off stdin echo so keystrokes don't appear on screen
// during progress rendering.
func disableEcho() {
	fd := int(os.Stdin.Fd())
	orig, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return
	}
	savedTermios = orig
	noecho := *orig
	noecho.Lflag &^= unix.ECHO
	unix.IoctlSetTermios(fd, unix.TCSETS, &noecho)
}

// restoreEcho restores the original terminal settings.
func restoreEcho() {
	if savedTermios != nil {
		unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TCSETS, savedTermios)
		savedTermios = nil
	}
}
