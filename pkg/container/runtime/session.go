package runtime

import (
	"context"

	"github.com/ahmetozer/sandal/pkg/lib/stdio"
)

// RunSession is what one run of a container needs besides the container's
// record (config.Config), and that only this run uses: the caller's context
// and streams, and the placement callback. It is passed next to the config
// down the run path (host.Run, the VM boot paths) and is never stored.
//
// A nil *RunSession, like a zero one, is the CLI: the process's terminal
// and signals, and no callback. Its methods are safe on nil.
type RunSession struct {
	// Ctx ends the run when it is cancelled: a foreground container is
	// killed and an image pull stops. Nil is context.Background().
	Ctx context.Context

	// IO is the streams and terminal of a caller that runs the container
	// in-process (sandal.RunContext). It is nil for the CLI and the daemon,
	// which use os.Stdin, os.Stdout, os.Stderr and process-wide signals.
	IO *stdio.Streams

	// OnPlaced, if set, is called once the container's pid is published
	// (see host.Run).
	OnPlaced func()
}

// Context returns Ctx, or context.Background() when there is none.
func (s *RunSession) Context() context.Context {
	if s == nil || s.Ctx == nil {
		return context.Background()
	}
	return s.Ctx
}

// Streams returns IO: nil for the CLI. Its methods fall back to the
// process's streams on nil.
func (s *RunSession) Streams() *stdio.Streams {
	if s == nil {
		return nil
	}
	return s.IO
}

// Placed calls OnPlaced, if set.
func (s *RunSession) Placed() {
	if s != nil && s.OnPlaced != nil {
		s.OnPlaced()
	}
}

// WithOnPlaced returns a copy of s whose OnPlaced calls f, then s's own
// OnPlaced. s is not changed.
func (s *RunSession) WithOnPlaced(f func()) *RunSession {
	var n RunSession
	if s != nil {
		n = *s
	}
	n.OnPlaced = func() {
		f()
		s.Placed()
	}
	return &n
}
