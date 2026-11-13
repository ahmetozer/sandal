package runtime

import (
	"errors"
	"fmt"
	"syscall"
	"testing"
)

func TestExitResult(t *testing.T) {
	if err := ExitResult(0, 0); err != nil {
		t.Errorf("ExitResult(0, 0) = %v, want nil", err)
	}
	for _, code := range []int{1, 5, 255} {
		err := ExitResult(code, 0)
		var exitErr *ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("ExitResult(%d, 0) = %v (%T), want *ExitError", code, err, err)
		}
		if exitErr.Code != code || exitErr.Signal != 0 {
			t.Errorf("ExitResult(%d, 0) = %+v", code, exitErr)
		}
		if want := fmt.Sprintf("container exited with code %d", code); err.Error() != want {
			t.Errorf("ExitResult(%d, 0).Error() = %q, want %q", code, err.Error(), want)
		}
	}
}

func TestExitResultSignal(t *testing.T) {
	for _, tc := range []struct {
		sig  syscall.Signal
		code int
	}{
		{syscall.SIGKILL, 137},
		{syscall.SIGTERM, 143},
		{syscall.SIGINT, 130},
	} {
		// A signalled process reports exit code -1.
		err := ExitResult(-1, tc.sig)
		var exitErr *ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("ExitResult(-1, %v) = %v (%T), want *ExitError", tc.sig, err, err)
		}
		if exitErr.Code != tc.code || exitErr.Signal != tc.sig {
			t.Errorf("ExitResult(-1, %v) = %+v, want Code %d", tc.sig, exitErr, tc.code)
		}
	}
}
