package sandal

import (
	"context"
	"errors"
	"fmt"

	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/lib/stdio"
)

// RunIO carries the streams of a caller that runs a container in-process
// (RunContext); nil fields fall back to os.Stdin, os.Stdout and os.Stderr.
type RunIO = stdio.Streams

// TermSizer is the caller's terminal in RunIO.Term: its size and resizes.
type TermSizer = stdio.TermSizer

// ExitError is returned by Run and RunContext when the foreground container
// ran and exited with a non-zero code (Code; 128+signal when a signal ended
// it, with Signal set). The CLI exits with that code.
type ExitError = crt.ExitError

// Run is the main entry point for `sandal run`.
// It handles platform dispatch (VM vs container) and flag parsing.
// CLI, daemon, and VM init all converge here.
func Run(args []string) error {
	return RunContext(context.Background(), args, nil)
}

// RunContext is Run for a caller inside a long-running process. With rio
// set, a foreground container uses rio's streams instead of the process's
// own, gets a TTY only from -t, and installs no process-wide signal
// handlers: cancelling ctx kills it instead, and rio.Term resizes its PTY.
// With rio nil it behaves as Run.
//
// It returns nil when the container exits 0, an *ExitError when it exits
// non-zero, and ctx's error when cancelling ctx ended it. A background
// container (-d) returns nil once started. A container in a VM (-vm)
// returns nil when the VM ends: the code of the container inside the guest
// does not reach the host.
func RunContext(ctx context.Context, args []string, rio *RunIO) error {
	if len(args) < 1 {
		return fmt.Errorf("no command option provided")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return runResult(ctx, platformRun(ctx, args, rio))
}

// runResult is err, except that the exit of a container killed because ctx
// was cancelled is ctx's error, as for exec.CommandContext.
func runResult(ctx context.Context, err error) error {
	var exitErr *ExitError
	if errors.As(err, &exitErr) && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
