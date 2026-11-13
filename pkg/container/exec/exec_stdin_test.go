//go:build linux

package exec

import (
	"bytes"
	"net"
	"os"
	osExec "os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runWithin runs cmd and fails the test if it has not returned after d.
func runWithin(t *testing.T, cmd *osExec.Cmd, d time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("%v still running %v after start", cmd.Args, d)
		return nil
	}
}

// unixConnPair returns the two ends of a connected unix stream socket, like
// the connection the VM controller hijacks for an exec.
func unixConnPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	var conns [2]net.Conn
	for i, fd := range fds {
		f := os.NewFile(uintptr(fd), "socketpair")
		conns[i], err = net.FileConn(f)
		f.Close()
		if err != nil {
			t.Fatalf("FileConn: %v", err)
		}
		t.Cleanup(func() { conns[i].Close() })
	}
	return conns[0], conns[1]
}

// The VM controller hands the exec its connection as stdin. A client whose
// stdin stays open never sends EOF, so the exec must end when the command
// exits instead of waiting for stdin to end.
func TestConnectStdinReturnsWhenCommandExits(t *testing.T) {
	stdin, _ := unixConnPair(t)

	cmd := osExec.Command("true")
	if err := connectStdin(cmd, stdin); err != nil {
		t.Fatalf("connectStdin: %v", err)
	}
	if err := runWithin(t, cmd, 5*time.Second); err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestConnectStdinDeliversInputAndEOF(t *testing.T) {
	cmd := osExec.Command("cat")
	if err := connectStdin(cmd, strings.NewReader("hello")); err != nil {
		t.Fatalf("connectStdin: %v", err)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := runWithin(t, cmd, 5*time.Second); err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.String() != "hello" {
		t.Fatalf("output = %q, want %q", out.String(), "hello")
	}
}
