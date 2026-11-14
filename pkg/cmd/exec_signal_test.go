//go:build linux || darwin

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

// TestExecSignalHelper is the `sandal exec` process of TestExecExitsOnSignal.
func TestExecSignalHelper(t *testing.T) {
	if os.Getenv("SANDAL_TEST_EXEC_HELPER") != "1" {
		t.Skip("helper process for TestExecExitsOnSignal")
	}
	err := ExecOnContainer([]string{"v", "--", "true"})
	fmt.Fprintf(os.Stderr, "ExecOnContainer returned: %v\n", err)
	os.Exit(3)
}

// A VM guest that never answers must not leave `sandal exec` unkillable:
// SIGTERM and SIGINT end it with the shell's 128+signal code.
func TestExecExitsOnSignal(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			// Short dir: sun_path is ~104 bytes on darwin.
			dir, err := os.MkdirTemp("/tmp", "sdl")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(dir) })
			lib, run := filepath.Join(dir, "lib"), filepath.Join(dir, "run")
			state := filepath.Join(lib, "state")
			for _, d := range []string{state, filepath.Join(run, "sock")} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			data, _ := json.Marshal(config.Config{Name: "v", VM: "kvm", Status: "running"})
			if err := os.WriteFile(filepath.Join(state, "v.json"), data, 0o644); err != nil {
				t.Fatal(err)
			}

			// The VM's management socket: takes the exec request, never answers.
			ln, err := net.Listen("unix", filepath.Join(run, "sock", "v.sock"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { ln.Close() })
			accepted := make(chan struct{})
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				t.Cleanup(func() { conn.Close() })
				close(accepted)
			}()

			cmd := exec.Command(os.Args[0], "-test.run=^TestExecSignalHelper$")
			cmd.Env = append(os.Environ(), "SANDAL_TEST_EXEC_HELPER=1",
				"SANDAL_LIB_DIR="+lib, "SANDAL_RUN_DIR="+run, "SANDAL_STATE_DIR="+state,
				"SANDAL_SOCKET="+filepath.Join(run, "sandal.sock"))
			stdin, err := cmd.StdinPipe() // open and silent, like a script's stdin
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { stdin.Close() })
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			t.Cleanup(func() { cmd.Process.Kill() })

			select {
			case <-accepted:
			case err := <-exited:
				t.Fatalf("exec exited before it reached the VM socket: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("exec did not connect to the VM socket")
			}
			// Signals are handled from before the dial, so exec is
			// ready for them once it has connected.
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}

			select {
			case err := <-exited:
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != 128+int(sig) {
					t.Fatalf("exec ended with %v, want exit code %d", err, 128+int(sig))
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("exec still running 5s after %v", sig)
			}
		})
	}
}
