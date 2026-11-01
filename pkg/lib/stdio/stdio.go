// Package stdio carries the standard streams and terminal of a caller that
// runs a container from inside a long-running process (such as sandal-api),
// in place of the process's own os.Stdin, os.Stdout and os.Stderr.
package stdio

import (
	"io"
	"os"
)

// TermSizer is the terminal of a caller: its size, and a channel that
// receives each time the size changes (what SIGWINCH is for a terminal).
type TermSizer interface {
	TermSize() (rows, cols uint16)
	Resized() <-chan struct{}
}

// Streams carries a caller's streams. Nil fields, and a nil *Streams, fall
// back to os.Stdin, os.Stdout and os.Stderr. Term is nil when the caller has
// no terminal.
type Streams struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Term           TermSizer
}

// In returns the caller's stdin, or os.Stdin.
func (s *Streams) In() io.Reader {
	if s == nil || s.Stdin == nil {
		return os.Stdin
	}
	return s.Stdin
}

// Out returns the caller's stdout, or os.Stdout.
func (s *Streams) Out() io.Writer {
	if s == nil || s.Stdout == nil {
		return os.Stdout
	}
	return s.Stdout
}

// Err returns the caller's stderr, or os.Stderr.
func (s *Streams) Err() io.Writer {
	if s == nil || s.Stderr == nil {
		return os.Stderr
	}
	return s.Stderr
}
