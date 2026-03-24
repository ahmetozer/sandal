//go:build linux

package terminal

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Terminal raw mode via ioctl (Linux)

const (
	tcgets = 0x5401
	tcsets = 0x5402
)

type termios struct {
	Iflag  uint32
	Oflag  uint32
	Cflag  uint32
	Lflag  uint32
	Line   uint8
	Cc     [19]uint8
	Ispeed uint32
	Ospeed uint32
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
	// Input flags: disable IGNBRK, BRKINT, PARMRK, ISTRIP, INLCR, IGNCR, ICRNL, IXON
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	// Output flags: disable OPOST
	raw.Oflag &^= syscall.OPOST
	// Local flags: disable ECHO, ECHONL, ICANON, ISIG, IEXTEN
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	// Control flags: disable CSIZE, PARENB; set CS8
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	// Control chars
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
		uintptr(tcgets),
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
		uintptr(tcsets),
		uintptr(unsafe.Pointer(t)),
	)
	if errno != 0 {
		return errno
	}
	return nil
}
