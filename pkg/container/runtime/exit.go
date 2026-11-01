package runtime

import (
	"fmt"
	"syscall"
)

// ExitError is the result of a foreground container that ran and exited
// with a non-zero code. It is not a failure of sandal itself: the CLI exits
// with Code instead of logging it.
//
// Code follows the shell convention: the container's own exit code, or
// 128+signal when a signal ended it (137 for SIGKILL), with Signal set.
// The container's recorded "exit N" status keeps -1 for a signal.
type ExitError struct {
	Code   int
	Signal syscall.Signal
}

func (e *ExitError) Error() string {
	if e.Signal != 0 {
		return fmt.Sprintf("container killed by signal %d (%v)", int(e.Signal), e.Signal)
	}
	return fmt.Sprintf("container exited with code %d", e.Code)
}

// ExitResult is the error for a container that exited with code, or that
// signal ended when it is not 0: nil for a clean exit 0, otherwise an
// *ExitError.
func ExitResult(code int, signal syscall.Signal) error {
	if signal != 0 {
		return &ExitError{Code: 128 + int(signal), Signal: signal}
	}
	if code == 0 {
		return nil
	}
	return &ExitError{Code: code}
}
