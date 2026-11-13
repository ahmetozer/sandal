//go:build linux || darwin

package sandal

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/vm/mgmt"
)

// fakeVMGuest serves the management socket of VM container name for one
// exec: it hands the request to answer, which writes the reply.
func fakeVMGuest(t *testing.T, name string, answer func(req *http.Request, conn net.Conn)) {
	t.Helper()
	// A short dir: unix socket paths are limited to ~104 bytes on macOS.
	dir, err := os.MkdirTemp("", "t10")
	if err != nil {
		t.Fatal(err)
	}
	old := env.RunDir
	env.RunDir = dir
	t.Cleanup(func() { env.RunDir = old; os.RemoveAll(dir) })
	os.MkdirAll(mgmt.SocketDir(), 0o755)
	ln, err := net.Listen("unix", mgmt.SocketPath(name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		req, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		answer(req, conn)
	}()
}

// framedGuest answers like a guest that knows the framed upgrade: "out"
// and then exit, when the request offers it.
func framedGuest(t *testing.T, exit mgmt.ExecExit) func(*http.Request, net.Conn) {
	return func(req *http.Request, conn net.Conn) {
		if !mgmt.OffersExecUpgrade(req.Header) {
			t.Errorf("request Upgrade = %q, want an offer of %s", req.Header.Values("Upgrade"), mgmt.ExecUpgrade)
			return
		}
		io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: "+mgmt.ExecUpgrade+"\r\n\r\n")
		w := mgmt.NewExecWriter(conn)
		io.WriteString(w, "out")
		w.Exit(exit)
	}
}

func execVM(t *testing.T, tty bool) (string, error) {
	t.Helper()
	var out strings.Builder
	c := &config.Config{Name: "t10v", VM: "kvm"}
	err := Exec(c, []string{"sh", "-c", "exit 3"}, "", "", tty, nil, strings.NewReader(""), &out, io.Discard)
	return out.String(), err
}

// The exit status the guest sends is the result of a VM exec, with and
// without a PTY, as for a native one.
func TestExecVMReturnsExitStatus(t *testing.T) {
	cases := []struct {
		name   string
		exit   mgmt.ExecExit
		code   int
		signal syscall.Signal
	}{
		{"exit 0", mgmt.ExecExit{}, 0, 0},
		{"exit 3", mgmt.ExecExit{Code: 3}, 3, 0},
		{"killed", mgmt.ExecExit{Code: 137, Signal: 9}, 137, syscall.SIGKILL},
	}
	for _, tty := range []bool{false, true} {
		for _, tc := range cases {
			fakeVMGuest(t, "t10v", framedGuest(t, tc.exit))
			out, err := execVM(t, tty)
			if out != "out" {
				t.Errorf("tty=%v %s: output = %q, want out", tty, tc.name, out)
			}
			if tc.code == 0 {
				if err != nil {
					t.Errorf("tty=%v %s: Exec = %v, want nil", tty, tc.name, err)
				}
				continue
			}
			var exitErr *ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("tty=%v %s: Exec = %v (%T), want *ExitError", tty, tc.name, err, err)
			}
			if exitErr.Code != tc.code || exitErr.Signal != tc.signal {
				t.Errorf("tty=%v %s: ExitError = %+v, want Code %d Signal %d", tty, tc.name, exitErr, tc.code, tc.signal)
			}
		}
	}
}

// A command the guest could not start is a sandal error, as natively.
func TestExecVMReturnsGuestError(t *testing.T) {
	fakeVMGuest(t, "t10v", framedGuest(t, mgmt.ExecExit{Code: 1, Error: "lookup nope in PATH"}))
	_, err := execVM(t, false)
	var exitErr *ExitError
	if err == nil || errors.As(err, &exitErr) || !strings.Contains(err.Error(), "lookup nope in PATH") {
		t.Fatalf("Exec = %v (%T), want the guest's error", err, err)
	}
}

// A guest that answers with the plain stream (it predates the framed
// upgrade) cannot report a status: its output is relayed and exec succeeds,
// as before.
func TestExecVMOldGuest(t *testing.T) {
	fakeVMGuest(t, "t10v", func(_ *http.Request, conn net.Conn) {
		io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: raw-stream\r\n\r\nout")
	})
	out, err := execVM(t, false)
	if err != nil || out != "out" {
		t.Fatalf("Exec = %v, output %q; want nil, out", err, out)
	}
}

// A framed stream that ends without the exit status (the VM went away) is
// an error, not a success.
func TestExecVMStreamCut(t *testing.T) {
	fakeVMGuest(t, "t10v", func(_ *http.Request, conn net.Conn) {
		io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: "+mgmt.ExecUpgrade+"\r\n\r\n")
		io.WriteString(mgmt.NewExecWriter(conn), "out")
	})
	out, err := execVM(t, true)
	if err == nil {
		t.Fatal("Exec = nil, want an error for the missing exit status")
	}
	if out != "out" {
		t.Errorf("output = %q, want out", out)
	}
}
