//go:build linux

package host

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ahmetozer/sandal/pkg/lib/stdio"
	"golang.org/x/sys/unix"
)

// runPiped runs name with the caller's streams the way crun does: pipes set
// up before Start, reaped with Process.Wait, output drained by finish.
func runPiped(s *stdio.Streams, name string, args ...string) (int, error) {
	cmd := exec.Command(name, args...)
	streams, err := pipeStreams(cmd, s)
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		streams.close()
		return 0, err
	}
	streams.started()
	state, err := cmd.Process.Wait()
	if err != nil {
		return 0, err
	}
	streams.finish(context.Background())
	return state.ExitCode(), nil
}

func TestPipeStreams(t *testing.T) {
	var out, errOut bytes.Buffer
	s := &stdio.Streams{Stdin: strings.NewReader("from caller\n"), Stdout: &out, Stderr: &errOut}
	code, err := runPiped(s, "sh", "-c", "cat; echo oops >&2; exit 3")
	if err != nil {
		t.Fatal(err)
	}
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
	if out.String() != "from caller\n" {
		t.Errorf("stdout = %q", out.String())
	}
	if errOut.String() != "oops\n" {
		t.Errorf("stderr = %q", errOut.String())
	}
}

// Stdout and stderr on one writer share one pipe, so the writer never
// sees concurrent Writes (bytes.Buffer is not safe for that).
func TestPipeStreamsSameWriter(t *testing.T) {
	var out bytes.Buffer
	s := &stdio.Streams{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out}
	cmd := exec.Command("sh", "-c", "echo a; echo b >&2")
	streams, err := pipeStreams(cmd, s)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Stdout != cmd.Stderr {
		t.Fatal("same writer got two pipes")
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	streams.started()
	cmd.Process.Wait()
	streams.finish(context.Background())
	if out.String() != "a\nb\n" {
		t.Errorf("output = %q", out.String())
	}
}

// A file stream goes to the child as it is, with no pipe.
func TestPipeStreamsFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cmd := exec.Command("true")
	streams, err := pipeStreams(cmd, &stdio.Streams{Stdout: f})
	if err != nil {
		t.Fatal(err)
	}
	defer streams.close()
	if cmd.Stdout != f || cmd.Stdin != os.Stdin || cmd.Stderr != os.Stderr {
		t.Fatalf("file streams were not handed over as they are: %v %v %v", cmd.Stdin, cmd.Stdout, cmd.Stderr)
	}
}

// A caller whose stdin never ends must not keep the run from returning.
func TestPipeStreamsOpenStdin(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	var out bytes.Buffer
	type result struct {
		code int
		err  error
	}
	done := make(chan result)
	go func() {
		code, err := runPiped(&stdio.Streams{Stdin: pr, Stdout: &out}, "sh", "-c", "echo hi")
		done <- result{code, err}
	}()
	select {
	case r := <-done:
		if r.err != nil || r.code != 0 || out.String() != "hi\n" {
			t.Errorf("code=%d err=%v out=%q", r.code, r.err, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return while the caller's stdin stayed open")
	}
}

func TestWatchCallerCancelKills(t *testing.T) {
	cmd := exec.Command("sleep", "600")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := watchCaller(ctx, cmd.Process, nil, nil)
	cancel()
	waited := make(chan *os.ProcessState)
	go func() {
		state, _ := cmd.Process.Wait()
		waited <- state
	}()
	select {
	case state := <-waited:
		stop()
		ws, ok := state.Sys().(syscall.WaitStatus)
		if !ok || !ws.Signaled() || ws.Signal() != syscall.SIGKILL {
			t.Errorf("child ended with %v, want SIGKILL", state)
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("cancelling ctx did not kill the child")
	}
}

// stop ends the watcher without touching the child.
func TestWatchCallerStop(t *testing.T) {
	cmd := exec.Command("sleep", "600")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	ctx, cancel := context.WithCancel(context.Background())
	watchCaller(ctx, cmd.Process, nil, nil)()
	cancel()
	time.Sleep(100 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("child gone after stop and a later cancel: %v", err)
	}
}

type fakeTerm struct {
	rows, cols uint16
	resized    chan struct{}
}

func (f *fakeTerm) TermSize() (uint16, uint16) { return f.rows, f.cols }
func (f *fakeTerm) Resized() <-chan struct{}   { return f.resized }

func TestWatchCallerResize(t *testing.T) {
	master, slave, err := allocPTY()
	if err != nil {
		t.Skipf("no PTY: %v", err)
	}
	defer master.Close()
	defer slave.Close()

	term := &fakeTerm{rows: 24, cols: 80, resized: make(chan struct{})}
	stop := watchCaller(context.Background(), &os.Process{Pid: -1}, master, term)
	defer stop()

	term.rows, term.cols = 50, 132
	term.resized <- struct{}{}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ws, err := unix.IoctlGetWinsize(int(slave.Fd()), unix.TIOCGWINSZ)
		if err == nil && ws.Row == 50 && ws.Col == 132 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("PTY size = %v (err %v), want 50x132", ws, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCallerTermSize(t *testing.T) {
	if r, c := callerTermSize(nil); r != 24 || c != 80 {
		t.Errorf("no term: %dx%d, want 24x80", r, c)
	}
	if r, c := callerTermSize(&fakeTerm{}); r != 24 || c != 80 {
		t.Errorf("zero size: %dx%d, want 24x80", r, c)
	}
	if r, c := callerTermSize(&fakeTerm{rows: 40, cols: 100}); r != 40 || c != 100 {
		t.Errorf("term: %dx%d, want 40x100", r, c)
	}
}
