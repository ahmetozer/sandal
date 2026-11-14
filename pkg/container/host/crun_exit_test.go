//go:build linux

package host

import (
	"os/exec"
	"syscall"
	"testing"
)

// crun reports a child's exit code, and the signal that ended it, from
// what Process.Wait returns.
func TestExitStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
		code   int
		signal syscall.Signal
	}{
		{"exit 0", "exit 0", 0, 0},
		{"exit 5", "exit 5", 5, 0},
		{"killed", "kill -KILL $$", -1, syscall.SIGKILL},
		{"terminated", "kill -TERM $$", -1, syscall.SIGTERM},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("sh", "-c", tc.script)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			state, err := cmd.Process.Wait()
			if err != nil {
				t.Fatal(err)
			}
			code, signal := exitStatus(state)
			if code != tc.code || signal != tc.signal {
				t.Errorf("exitStatus = %d, %v; want %d, %v", code, signal, tc.code, tc.signal)
			}
		})
	}

	// A failed Wait has no state.
	if code, signal := exitStatus(nil); code != -1 || signal != 0 {
		t.Errorf("exitStatus(nil) = %d, %v; want -1, 0", code, signal)
	}
}
