//go:build linux || darwin

package sandal

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/vm/mgmt"
)

// fakeVMSocket listens on the management socket of VM container name in a
// short temp run dir (sun_path is ~104 bytes on darwin, too short for
// t.TempDir()) and hands each request's connection to guest.
func fakeVMSocket(t *testing.T, name string, guest func(net.Conn)) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sdl")
	if err != nil {
		t.Fatal(err)
	}
	prev := env.RunDir
	env.RunDir = dir
	t.Cleanup(func() {
		env.RunDir = prev
		os.RemoveAll(dir)
	})
	if err := os.MkdirAll(mgmt.SocketDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", mgmt.SocketPath(name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { conn.Close() })
			if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
				conn.Close()
				continue
			}
			guest(conn)
		}
	}()
}

// execWithin runs execViaMgmt for container name and fails the test if it
// has not returned after d.
func execWithin(t *testing.T, name string, d time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- execViaMgmt(name, []string{"true"}, "", "", false, nil, strings.NewReader(""), io.Discard)
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("exec still waiting %v after start", d)
		return nil
	}
}

func TestExecViaMgmtGuestDoesNotAnswer(t *testing.T) {
	prev := execReplyTimeout
	execReplyTimeout = 200 * time.Millisecond
	t.Cleanup(func() { execReplyTimeout = prev })
	fakeVMSocket(t, "v", func(net.Conn) {}) // reads the request, never answers

	err := execWithin(t, "v", 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "the VM guest did not answer") {
		t.Fatalf("err = %v, want the VM guest did not answer", err)
	}
}

func TestExecViaMgmtGuestClosesWithoutAnswer(t *testing.T) {
	fakeVMSocket(t, "v", func(c net.Conn) { c.Close() })

	err := execWithin(t, "v", 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "the VM guest did not answer") {
		t.Fatalf("err = %v, want the VM guest did not answer", err)
	}
}
