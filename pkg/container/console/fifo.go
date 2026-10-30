//go:build linux

package console

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// SetupFIFO creates console files for a daemonless background container.
// stdin is a FIFO (opened O_RDWR so reads block without EOF on detach).
// stdout and stderr are regular files that can be tailed by attach.
// Returns files to use as cmd.Stdin, cmd.Stdout, cmd.Stderr and a cleanup func.
func SetupFIFO(name string) (stdin, stdout, stderr *os.File, cleanup func(), err error) {
	dir := Dir(name)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("creating console dir: %w", err)
	}

	// Create stdin FIFO
	stdinPath := StdinPath(name)
	os.Remove(stdinPath) // remove stale
	if err = unix.Mkfifo(stdinPath, 0o600); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("creating stdin fifo: %w", err)
	}

	// Open stdin FIFO with O_RDWR so:
	// - the child's reads block when no attach is connected (no data)
	// - closing the attach-side writer doesn't cause EOF (child still has write fd)
	stdin, err = os.OpenFile(stdinPath, os.O_RDWR, 0)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("opening stdin fifo: %w", err)
	}

	// stdout/stderr are regular files
	stdout, err = os.OpenFile(StdoutPath(name), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		stdin.Close()
		return nil, nil, nil, nil, fmt.Errorf("creating stdout file: %w", err)
	}

	stderr, err = os.OpenFile(StderrPath(name), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		stdin.Close()
		stdout.Close()
		return nil, nil, nil, nil, fmt.Errorf("creating stderr file: %w", err)
	}

	// Write mode marker
	os.WriteFile(ModePath(name), []byte(ModeFIFO), 0o644)

	cleanup = func() {
		stdin.Close()
		stdout.Close()
		stderr.Close()
	}

	return stdin, stdout, stderr, cleanup, nil
}

// AttachFIFO connects the host terminal to a FIFO-based console.
// It tails stdout/stderr files and writes to the stdin FIFO.
// Returns when the done channel is closed or an error occurs.
func AttachFIFO(name string, hostStdin io.Reader, hostStdout, hostStderr io.Writer, done <-chan struct{}) error {
	// Open stdin FIFO for writing (sends data to the container's stdin)
	stdinFIFO, err := os.OpenFile(StdinPath(name), os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("opening stdin fifo: %w", err)
	}
	defer stdinFIFO.Close()

	// Open stdout for tailing
	stdoutFile, err := os.Open(StdoutPath(name))
	if err != nil {
		return fmt.Errorf("opening stdout: %w", err)
	}
	defer stdoutFile.Close()

	// Open stderr for tailing
	stderrFile, err := os.Open(StderrPath(name))
	if err != nil {
		return fmt.Errorf("opening stderr: %w", err)
	}
	defer stderrFile.Close()

	// Seek to end — only show new output
	stdoutFile.Seek(0, io.SeekEnd)
	stderrFile.Seek(0, io.SeekEnd)

	// Relay stdin → FIFO; Ctrl+P, Ctrl+Q detaches
	detach := make(chan struct{})
	go func() {
		if copyDetach(stdinFIFO, hostStdin) {
			close(detach)
		}
	}()

	// stop ends the tails on detach as well as on done
	stop := make(chan struct{})
	defer close(stop)

	// Tail stdout → host stdout
	go tailFile(stdoutFile, hostStdout, stop)

	// Tail stderr → host stderr
	go tailFile(stderrFile, hostStderr, stop)

	select {
	case <-done:
	case <-detach:
	}
	return nil
}

// copyDetach copies src to dst until src ends, a write fails or Ctrl+P,
// Ctrl+Q is read, and reports whether it stopped on the detach keys.
// As in socket mode, the Ctrl+P is forwarded and the Ctrl+Q is not.
func copyDetach(dst io.Writer, src io.Reader) bool {
	buf := make([]byte, 4096)
	prevCtrlP := false
	for {
		n, err := src.Read(buf)
		for i, b := range buf[:n] {
			if prevCtrlP && b == 0x11 { // Ctrl+Q
				dst.Write(buf[:i])
				return true
			}
			prevCtrlP = b == 0x10 // Ctrl+P
		}
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return false
			}
		}
		if err != nil {
			return false
		}
	}
}

// SetupTee gives a foreground process, such as a foreground KVM VMM, a FIFO
// console that `sandal attach` can join. Output goes to w and, best effort,
// to the stdout log; input merges r and the stdin FIFO. EOF on r does not
// end in, so attachers can still type. cleanup ends in and closes the
// console files.
func SetupTee(name string, r io.Reader, w io.Writer) (in io.Reader, out io.Writer, cleanup func(), err error) {
	fin, fout, _, fifoCleanup, err := SetupFIFO(name)
	if err != nil {
		return nil, nil, nil, err
	}

	// io.Pipe serializes the two writers
	pr, pw := io.Pipe()
	go io.Copy(pw, r)
	// The FIFO is opened O_RDWR, so it never reaches EOF while open
	go io.Copy(pw, fin)

	cleanup = func() {
		pw.Close()
		fifoCleanup()
	}
	return pr, &teeWriter{primary: w, log: fout}, cleanup, nil
}

// teeWriter writes to primary and copies to log. Log write errors are
// ignored: unlike io.MultiWriter, a full disk never stops the primary.
type teeWriter struct {
	primary io.Writer
	log     io.Writer
}

func (t *teeWriter) Write(p []byte) (int, error) {
	n, err := t.primary.Write(p)
	t.log.Write(p)
	return n, err
}

// tailFile reads from f in a loop, writing to w, until done is closed.
func tailFile(f *os.File, w io.Writer, done <-chan struct{}) {
	buf := make([]byte, 4096)
	for {
		select {
		case <-done:
			return
		default:
		}
		n, err := f.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
		}
		if err != nil {
			// EOF means no new data yet — small sleep via select
			select {
			case <-done:
				return
			default:
				// Brief yield to avoid busy-spinning on EOF
				unix.Nanosleep(&unix.Timespec{Nsec: 50_000_000}, nil) // 50ms
			}
		}
	}
}
