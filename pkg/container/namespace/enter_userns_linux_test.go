//go:build linux

package namespace

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
)

// nsUserNew is a container started with -ns-user "": new user and uts
// namespaces, the rest shared with the host.
func nsUserNew(t *testing.T) Namespaces {
	t.Helper()
	ns := Namespaces{}
	for name := range namespaceList {
		v := "host"
		if name == "user" || name == "uts" {
			v = ""
		}
		ns[name] = NamespaceConf{UserValue: &v}
	}
	if err := ns.Defaults(); err != nil {
		t.Fatal(err)
	}
	return ns
}

// TestEnterUserNSContainer enters a process that runs in its own user
// namespace, as `sandal exec` does for a -ns-user "" container: a Go process
// cannot setns into a user namespace (the kernel refuses a multithreaded
// caller), so ForExec leaves it out and Enter joins the other kinds.
func TestEnterUserNSContainer(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("e2e: requires root")
	}
	idmap := []syscall.SysProcIDMap{{ContainerID: 0, HostID: 0, Size: 65536}}
	cont := exec.Command("sleep", "30")
	cont.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:  syscall.CLONE_NEWUSER | syscall.CLONE_NEWUTS,
		UidMappings: idmap,
		GidMappings: idmap,
	}
	if err := cont.Start(); err != nil {
		t.Fatalf("start container process: %v", err)
	}
	t.Cleanup(func() {
		_ = cont.Process.Kill()
		_ = cont.Wait()
	})
	pid := cont.Process.Pid
	ns, err := ForExec(pid, nsUserNew(t), nil)
	if err != nil {
		t.Fatalf("ForExec: %v", err)
	}

	errc := make(chan error, 1)
	go func() {
		// The thread is left in the container's namespaces: never
		// unlocked, so the runtime drops it when this goroutine ends.
		runtime.LockOSThread()
		if err := Enter(pid, ns); err != nil {
			errc <- err
			return
		}
		self, _ := os.Readlink("/proc/thread-self/ns/uts")
		want, _ := os.Readlink(fmt.Sprintf("/proc/%d/ns/uts", pid))
		if self != want {
			errc <- fmt.Errorf("uts namespace = %s, want the container's %s", self, want)
			return
		}
		errc <- nil
	}()
	if err := <-errc; err != nil {
		t.Fatalf("Enter: %v", err)
	}
}
