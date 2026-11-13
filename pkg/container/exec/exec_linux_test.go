package exec

import (
	"io"
	osExec "os/exec"
	"strings"
	"syscall"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
)

// Until its init has pivoted into the container root, /proc/<pid>/root of
// a container is still the host's root, so exec must not join a container
// that is still being created.
func TestExecRefusesContainerStillCreating(t *testing.T) {
	// Stand-in init in namespaces of its own, so nothing here can touch the
	// host's hostname or mounts if exec did join it.
	init := osExec.Command("sleep", "30")
	init.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNS | syscall.CLONE_NEWUTS |
		syscall.CLONE_NEWIPC | syscall.CLONE_NEWNET | syscall.CLONE_NEWPID | syscall.CLONE_NEWCGROUP}
	if err := init.Start(); err != nil {
		t.Skipf("cannot start a process in new namespaces (needs root): %v", err)
	}
	defer func() {
		init.Process.Kill()
		init.Wait()
	}()

	c := &config.Config{Name: "t7creating", ContPid: init.Process.Pid, Status: crt.ContainerStatusCreating}
	done := make(chan error, 1)
	// ExecInContainer retires its OS thread when it joins, so run it on a
	// goroutine of its own.
	go func() { done <- ExecInContainer(c, []string{"true"}, "", "", false, nil, nil, io.Discard, io.Discard) }()
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "still starting") {
		t.Fatalf("ExecInContainer(creating) = %v, want a \"still starting\" error", err)
	}
}
