//go:build linux

package console

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ahmetozer/sandal/pkg/env"
)

// chunkReader returns one chunk per Read, then EOF.
type chunkReader struct{ chunks []string }

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[0])
	r.chunks = r.chunks[1:]
	return n, nil
}

func TestCopyDetach(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
		detach bool
		want   string
	}{
		{"same chunk", []string{"ls\x10\x11after"}, true, "ls\x10"},
		{"split chunks", []string{"ls\x10", "\x11after"}, true, "ls\x10"},
		{"lone ctrl+p", []string{"a\x10b", "\x11"}, false, "a\x10b\x11"},
		{"no keys", []string{"abc", "def"}, false, "abcdef"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var dst bytes.Buffer
			got := copyDetach(&dst, &chunkReader{chunks: tt.chunks})
			if got != tt.detach {
				t.Errorf("detached = %v, want %v", got, tt.detach)
			}
			if dst.String() != tt.want {
				t.Errorf("dst = %q, want %q", dst.String(), tt.want)
			}
		})
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestTeeWriterIgnoresLogError(t *testing.T) {
	var primary bytes.Buffer
	w := &teeWriter{primary: &primary, log: failWriter{}}
	for _, s := range []string{"one ", "two"} {
		if n, err := w.Write([]byte(s)); err != nil || n != len(s) {
			t.Fatalf("Write(%q) = %d, %v", s, n, err)
		}
	}
	if primary.String() != "one two" {
		t.Errorf("primary = %q, want %q", primary.String(), "one two")
	}
}

func setRunDir(t *testing.T) {
	t.Helper()
	old := env.RunDir
	env.RunDir = t.TempDir()
	t.Cleanup(func() { env.RunDir = old })
}

func TestSetupTee(t *testing.T) {
	setRunDir(t)
	var primary bytes.Buffer
	in, out, cleanup, err := SetupTee("c1", strings.NewReader("abc"), &primary)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if mode, _ := os.ReadFile(ModePath("c1")); string(mode) != ModeFIFO {
		t.Errorf("mode = %q, want %q", mode, ModeFIFO)
	}

	// r ends at once; input from the stdin FIFO must still come through.
	fifo, err := os.OpenFile(StdinPath("c1"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fifo.Close()
	if _, err := fifo.Write([]byte("xyz")); err != nil {
		t.Fatal(err)
	}

	got := make([]byte, 0, 6)
	buf := make([]byte, 6)
	for len(got) < 6 {
		n, err := in.Read(buf)
		if err != nil {
			t.Fatalf("read in after %q: %v", got, err)
		}
		got = append(got, buf[:n]...)
	}
	if s := string(got); !strings.Contains(s, "abc") || !strings.Contains(s, "xyz") {
		t.Errorf("in = %q, want abc and xyz", s)
	}

	if _, err := out.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if primary.String() != "hello" {
		t.Errorf("primary = %q, want %q", primary.String(), "hello")
	}
	if log, _ := os.ReadFile(StdoutPath("c1")); string(log) != "hello" {
		t.Errorf("stdout log = %q, want %q", log, "hello")
	}

	cleanup()
	if _, err := in.Read(buf); err != io.EOF {
		t.Errorf("read in after cleanup: %v, want EOF", err)
	}
}

// syncBuffer is a bytes.Buffer safe for the tail goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestAttachFIFODetach(t *testing.T) {
	setRunDir(t)
	stdin, stdout, _, cleanup, err := SetupFIFO("c1")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	hostIn, hostInW := io.Pipe()
	defer hostInW.Close()
	var hostOut, hostErr syncBuffer
	ret := make(chan error, 1)
	go func() { ret <- AttachFIFO("c1", hostIn, &hostOut, &hostErr, nil) }()

	// Input reaches the container's stdin FIFO.
	go hostInW.Write([]byte("ls\n"))
	buf := make([]byte, 16)
	stdin.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := stdin.Read(buf); err != nil || string(buf[:n]) != "ls\n" {
		t.Fatalf("container stdin = %q, %v; want %q", buf[:n], err, "ls\n")
	}

	// Output written after attach is relayed. AttachFIFO starts at the
	// end of the log, so keep writing until the tail picks it up.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(hostOut.String(), "out") {
		if time.Now().After(deadline) {
			t.Fatalf("host stdout = %q, want %q", hostOut.String(), "out")
		}
		stdout.Write([]byte("out"))
		time.Sleep(100 * time.Millisecond)
	}

	// Ctrl+P, Ctrl+Q returns even with a nil done channel.
	go hostInW.Write([]byte{0x10, 0x11})
	select {
	case err := <-ret:
		if err != nil {
			t.Errorf("AttachFIFO: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AttachFIFO did not return on Ctrl+P, Ctrl+Q")
	}
}
