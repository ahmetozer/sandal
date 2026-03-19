//go:build linux

package cruntime

import (
	"fmt"
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// allocPTY opens a new PTY master/slave pair.
// The caller must close both files when done.
func allocPTY() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}

	// Unlock the slave
	unlock := 0
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, master.Fd(), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		master.Close()
		return nil, nil, fmt.Errorf("TIOCSPTLCK: %w", errno)
	}

	// Get slave PTY number
	var ptn uint32
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, master.Fd(), unix.TIOCGPTN, uintptr(unsafe.Pointer(&ptn))); errno != 0 {
		master.Close()
		return nil, nil, fmt.Errorf("TIOCGPTN: %w", errno)
	}

	slavePath := fmt.Sprintf("/dev/pts/%d", ptn)
	slave, err = os.OpenFile(slavePath, os.O_RDWR, 0)
	if err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("open %s: %w", slavePath, err)
	}

	// Configure the slave PTY for sane defaults:
	// - Keep OPOST and ONLCR so \n is translated to \r\n on output
	// - Disable ECHO so escape sequence responses don't appear as garbage
	// - Keep ISIG so Ctrl+C generates SIGINT for the container process
	// - Keep ICANON off so input is byte-at-a-time (shells handle editing)
	if termios, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS); err == nil {
		termios.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
		termios.Oflag |= unix.OPOST | unix.ONLCR
		termios.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.IEXTEN
		termios.Lflag |= unix.ISIG
		termios.Cflag &^= unix.CSIZE | unix.PARENB
		termios.Cflag |= unix.CS8
		termios.Cc[unix.VMIN] = 1
		termios.Cc[unix.VTIME] = 0
		unix.IoctlSetTermios(int(slave.Fd()), unix.TCSETS, termios)
	}

	return master, slave, nil
}

// setPTYSize sets the terminal window size on the PTY master.
func setPTYSize(master *os.File, rows, cols uint16) {
	ws := unix.Winsize{Row: rows, Col: cols}
	unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &ws)
}

// startPTYRelay starts bidirectional I/O relay between the PTY master
// and the given stdin/stdout (typically the serial console).
// Returns a function that waits for the output relay to finish.
func startPTYRelay(master *os.File, stdin io.Reader, stdout io.Writer) func() {
	done := make(chan struct{})

	// master → stdout (terminates when slave closes → master read returns EIO)
	go func() {
		io.Copy(stdout, master)
		close(done)
	}()

	// stdin → master (may block on stdin.Read indefinitely; abandoned on exit)
	go func() {
		io.Copy(master, stdin)
	}()

	return func() {
		<-done
	}
}
