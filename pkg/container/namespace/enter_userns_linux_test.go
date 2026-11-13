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

// TestSetEmptyToPidKeepsCallerUserNS: a Go process cannot setns into a user
// namespace (the kernel refuses a multithreaded caller), so Enter keeps the
// caller's and joins the container's other namespaces.
func TestSetEmptyToPidKeepsCallerUserNS(t *testing.T) {
	got := nsUserNew(t).SetEmptyToPid(42)

	if uts := got.Get("uts"); !uts.IsUserDefined || uts.String() != "pid:42" {
		t.Fatalf("uts = %q (user-defined %v), want joined pid:42", uts.String(), uts.IsUserDefined)
	}
	if user := got.Get("user"); user.IsUserDefined || !user.IsHost {
		t.Fatalf("user = %q (user-defined %v), want the caller's (host)", user.String(), user.IsUserDefined)
	}
	// Enter unshares these: a new user namespace fails just the same.
	if flags := got.Cloneflags(); flags != 0 {
		t.Fatalf("Cloneflags = %#x, want 0", flags)
	}
}

// TestEnterUserNSContainer enters a process that runs in its own user
// namespace, as `sandal exec` does for a -ns-user "" container.
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
	ns := nsUserNew(t)

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
