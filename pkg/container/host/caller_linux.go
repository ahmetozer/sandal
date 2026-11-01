//go:build linux

package host

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/lib/stdio"
)

// callerStreams wires a foreground container's stdio to the streams of an
// in-process caller (config.Config.IO). A stream that is an *os.File goes to
// the child as it is; any other reader or writer is copied through a pipe
// here. exec.Cmd would copy them itself, but only Cmd.Wait waits for those
// copies, and crun reaps the child with Process.Wait.
type callerStreams struct {
	childEnds []*os.File // the child's pipe ends, closed here once it runs
	stdinW    *os.File   // write end of the stdin pipe, nil without one
	outR      []*os.File // read ends of the stdout/stderr pipes
	copied    sync.WaitGroup
}

// pipeStreams sets cmd's stdin, stdout and stderr from s. Call started once
// the child runs (or close when it did not start), and finish after it is
// reaped.
func pipeStreams(cmd *exec.Cmd, s *stdio.Streams) (*callerStreams, error) {
	cs := &callerStreams{}
	if in, ok := s.In().(*os.File); ok {
		cmd.Stdin = in
	} else {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		cmd.Stdin = r
		cs.childEnds = append(cs.childEnds, r)
		cs.stdinW = w
		// Ends at EOF, or blocks in Read after the child is gone until the
		// caller closes its stdin, as the PTY relay's stdin copy does.
		go func(in io.Reader) {
			io.Copy(w, in)
			w.Close()
		}(s.In())
	}

	var err error
	if cmd.Stdout, err = cs.output(s.Out()); err != nil {
		cs.close()
		return nil, err
	}
	// One pipe when both are the same writer, so it never sees two Writes
	// at once (exec.Cmd does the same).
	if sameWriter(s.Out(), s.Err()) {
		cmd.Stderr = cmd.Stdout
	} else if cmd.Stderr, err = cs.output(s.Err()); err != nil {
		cs.close()
		return nil, err
	}
	return cs, nil
}

// output returns what the child writes to for w: w itself when it is a file,
// otherwise a pipe copied into w.
func (cs *callerStreams) output(w io.Writer) (io.Writer, error) {
	if f, ok := w.(*os.File); ok {
		return f, nil
	}
	r, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cs.childEnds = append(cs.childEnds, pw)
	cs.outR = append(cs.outR, r)
	cs.copied.Add(1)
	go func() {
		defer cs.copied.Done()
		io.Copy(w, r)
		r.Close()
	}()
	return pw, nil
}

// started closes the parent's copies of the child's pipe ends, so the
// output copies end when the child (and anything it left holding them) does.
func (cs *callerStreams) started() {
	for _, f := range cs.childEnds {
		f.Close()
	}
	cs.childEnds = nil
}

// close releases the pipes of a child that never started.
func (cs *callerStreams) close() {
	cs.started()
	cs.finish(context.Background())
}

// finish waits until the child's output is copied to the caller, and stops
// feeding it stdin. If ctx ends first (a process outside the container's
// pid namespace may still hold the pipes) the pipes are closed instead, so
// nothing is written to the caller's streams after crun returns.
func (cs *callerStreams) finish(ctx context.Context) {
	if cs.stdinW != nil {
		cs.stdinW.Close()
	}
	done := make(chan struct{})
	go func() {
		cs.copied.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		for _, r := range cs.outR {
			r.Close()
		}
		<-done
	}
}

// sameWriter reports whether a and b are the same writer; false for
// writers of a type that cannot be compared.
func sameWriter(a, b io.Writer) (same bool) {
	defer func() { recover() }()
	return a == b
}

// watchCaller stands in for the CLI's signal forwarding for an in-process
// caller, without process-wide signal handlers: cancelling ctx kills the
// container, as SIGINT/SIGTERM do for the CLI, and term's resizes go to the
// PTY (ptmx, nil without one). The returned stop ends it; call it once the
// child is reaped and before ptmx is closed.
func watchCaller(ctx context.Context, proc *os.Process, ptmx *os.File, term stdio.TermSizer) (stop func()) {
	var resized <-chan struct{}
	if ptmx != nil && term != nil {
		resized = term.Resized()
	}
	quit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-quit:
				return
			case <-ctx.Done():
				// SIGKILL as for SIGINT/SIGTERM: PID 1 in a namespace
				// ignores signals it has no handler for.
				if err := proc.Signal(syscall.SIGKILL); err != nil {
					slog.Debug("failed to kill container on cancel", "error", err)
				} else {
					slog.Debug("killed container on cancel", "pid", proc.Pid, "cause", context.Cause(ctx))
				}
				return
			case _, ok := <-resized:
				if !ok {
					resized = nil
					continue
				}
				rows, cols := callerTermSize(term)
				setPTYSize(ptmx, rows, cols)
			}
		}
	}()
	return func() {
		close(quit)
		<-done
	}
}

// callerTermSize is the size of the caller's terminal, or 24x80 when it
// has none or reports no size.
func callerTermSize(term stdio.TermSizer) (rows, cols uint16) {
	if term != nil {
		if rows, cols = term.TermSize(); rows > 0 && cols > 0 {
			return rows, cols
		}
	}
	return 24, 80
}
