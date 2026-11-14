//go:build linux

package main

import (
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// As PID 1 (the VM guest init), sandal serves exec from goroutines that
// lock their thread and move it into a container's namespaces. The runtime
// parks the main thread instead of discarding it, so no such goroutine may
// get the main thread.
func TestPID1KeepsMainThread(t *testing.T) {
	if os.Getenv("SANDAL_TEST_PID1_HELPER") == "1" {
		lockedThreadsAvoidMain(t)
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root to run the helper as PID 1 of a new pid namespace")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPID1KeepsMainThread$", "-test.v")
	cmd.Env = append(os.Environ(), "SANDAL_TEST_PID1_HELPER=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWPID}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper as PID 1: %v\n%s", err, out)
	}
}

// lockedThreadsAvoidMain locks many goroutines to their threads at once,
// like concurrent exec handlers, and fails if one of them got the main
// thread.
func lockedThreadsAvoidMain(t *testing.T) {
	if os.Getpid() != 1 {
		t.Fatalf("helper pid = %d, want 1", os.Getpid())
	}
	const n = 64
	tids := make(chan int, n)
	release := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			tids <- unix.Gettid()
			<-release
		}()
	}
	got := make([]int, 0, n)
	for i := 0; i < n; i++ {
		got = append(got, <-tids)
	}
	close(release)
	wg.Wait()
	for _, tid := range got {
		if tid == os.Getpid() {
			t.Fatalf("a goroutine locked the main thread (tid %d) of PID 1", tid)
		}
	}
}
