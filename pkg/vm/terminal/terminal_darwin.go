//go:build darwin

package terminal

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Terminal raw mode via ioctl (macOS)

type termios struct {
	Iflag  uint64
	Oflag  uint64
	Cflag  uint64
	Lflag  uint64
	Cc     [20]uint8
	Ispeed uint64
	Ospeed uint64
}

// SetRaw puts stdin into raw mode for serial console I/O and returns
// a function that restores the original terminal settings.
func SetRaw() (restore func(), err error) {
	fd := os.Stdin.Fd()
	var orig termios
	if err := tcgetattr(fd, &orig); err != nil {
		return nil, fmt.Errorf("tcgetattr: %w", err)
	}

	raw := orig
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Iflag &^= syscall.IXON | syscall.ICRNL | syscall.BRKINT | syscall.INPCK | syscall.ISTRIP
	raw.Oflag &^= syscall.OPOST
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0

	if err := tcsetattr(fd, &raw); err != nil {
		return nil, fmt.Errorf("tcsetattr: %w", err)
	}

	return func() {
		tcsetattr(fd, &orig)
	}, nil
}

func tcgetattr(fd uintptr, t *termios) error {
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		uintptr(syscall.TIOCGETA),
		uintptr(unsafe.Pointer(t)),
	)
	if errno != 0 {
		return errno
	}
	return nil
}

func tcsetattr(fd uintptr, t *termios) error {
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		uintptr(syscall.TIOCSETA),
		uintptr(unsafe.Pointer(t)),
	)
	if errno != 0 {
		return errno
	}
	return nil
}
