//go:build linux

package net

import (
	"fmt"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// errIsEEXIST must classify "already exists" without panicking on wrapped
// errors. The old code used `err.(unix.Errno)` (single-value assertion), which
// panics for any non-bare-Errno error (e.g. a fmt.wrapError or *os.SyscallError
// returned from the netlink receive path), crashing the process.
func TestErrIsEEXIST(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"bare EEXIST", unix.EEXIST, true},
		{"bare syscall EEXIST", syscall.EEXIST, true},
		{"wrapped EEXIST", fmt.Errorf("add addr: %w", unix.EEXIST), true},
		{"other bare errno", unix.ENOBUFS, false},
		{"wrapped non-errno (would panic old code)", fmt.Errorf("wrong sender portid"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := errIsEEXIST(tc.err); got != tc.want {
				t.Fatalf("errIsEEXIST(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
