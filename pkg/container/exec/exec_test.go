//go:build linux

package exec

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	osExec "os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/namespace"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
)

// utsSandbox starts a process in a new UTS namespace and returns a
// container config for it whose exec joins only that namespace. Every other
// kind stays on the host, so ExecInContainer runs the host's sh and its
// Sethostname changes the sandbox, not the machine running the test.
func utsSandbox(t *testing.T) *config.Config {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("joining a namespace needs root")
	}
	sleeper := osExec.Command("sleep", "60")
	sleeper.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWUTS}
	if err := sleeper.Start(); err != nil {
		t.Fatalf("start sandbox: %v", err)
	}
	t.Cleanup(func() {
		sleeper.Process.Kill()
		sleeper.Wait()
	})

	target := fmt.Sprintf("pid:%d", sleeper.Process.Pid)
	host := namespace.NamespaceConf{IsHost: true}
	return &config.Config{
		Name:    "t10exec",
		ContPid: sleeper.Process.Pid,
		NS: namespace.Namespaces{
			"mnt": host, "pid": host, "net": host, "ipc": host, "cgroup": host,
			"uts": {UserValue: &target, IsUserDefined: true},
		},
	}
}

// execSh runs sh -c script through ExecInContainer on its own goroutine,
// whose OS thread is retired with the namespaces it joined.
func execSh(t *testing.T, c *config.Config, tty bool, script string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- ExecInContainer(c, c.NS, []string{"sh", "-c", script}, "", "", tty, nil,
			strings.NewReader(""), &out, &out)
	}()
	select {
	case err := <-done:
		return out.String(), err
	case <-time.After(20 * time.Second):
		t.Fatal("ExecInContainer did not return")
		return "", nil
	}
}

// The command's exit status is the result of the exec, with and without a
// PTY: nil for 0, otherwise *runtime.ExitError with the shell convention
// (the code, or 128+signal).
func TestExecInContainerExitStatus(t *testing.T) {
	c := utsSandbox(t)
	cases := []struct {
		name   string
		script string
		code   int
		signal syscall.Signal
	}{
		{"exit 0", "echo out; exit 0", 0, 0},
		{"exit 3", "echo out; exit 3", 3, 0},
		{"killed", "echo out; kill -KILL $$", 137, syscall.SIGKILL},
	}
	for _, tty := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("tty=%v/%s", tty, tc.name), func(t *testing.T) {
				out, err := execSh(t, c, tty, tc.script)
				if !strings.Contains(out, "out") {
					t.Errorf("output = %q, want it to contain %q", out, "out")
				}
				if tc.code == 0 {
					if err != nil {
						t.Fatalf("ExecInContainer = %v, want nil", err)
					}
					return
				}
				var exitErr *crt.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatalf("ExecInContainer = %v (%T), want *runtime.ExitError", err, err)
				}
				if exitErr.Code != tc.code || exitErr.Signal != tc.signal {
					t.Errorf("ExitError = %+v, want Code %d Signal %d", exitErr, tc.code, tc.signal)
				}
			})
		}
	}
}

// A command that cannot be started is a sandal error, not an exit status.
func TestExecInContainerStartError(t *testing.T) {
	c := utsSandbox(t)
	done := make(chan error, 1)
	go func() {
		done <- ExecInContainer(c, c.NS, []string{"/nonexistent/t10"}, "", "", false, nil,
			strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	}()
	err := <-done
	var exitErr *crt.ExitError
	if err == nil || errors.As(err, &exitErr) {
		t.Fatalf("ExecInContainer = %v (%T), want a start error", err, err)
	}
}
