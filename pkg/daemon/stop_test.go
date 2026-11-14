//go:build linux

package daemon

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
)

// stopTestProc is a child standing in for a container's process.
type stopTestProc struct {
	pid    int
	start  uint64
	exited chan struct{}
}

func startStopTestProc(t *testing.T) *stopTestProc {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &stopTestProc{pid: cmd.Process.Pid, exited: make(chan struct{})}
	go func() { cmd.Wait(); close(p.exited) }()
	t.Cleanup(func() { cmd.Process.Kill(); <-p.exited })
	st, err := crt.ProcessStartTime(p.pid)
	if err != nil {
		t.Fatal(err)
	}
	p.start = st
	return p
}

func (p *stopTestProc) exitsWithin(d time.Duration) bool {
	select {
	case <-p.exited:
		return true
	case <-time.After(d):
		return false
	}
}

// useStopTestState points the controller at a temp state dir holding a
// detached native startup container and a startup VM, both alive.
func useStopTestState(t *testing.T) (native, vm *stopTestProc) {
	t.Helper()
	dir := t.TempDir()
	prevState, prevSock := env.BaseStateDir, env.DaemonSocket
	env.BaseStateDir = filepath.Join(dir, "state")
	env.DaemonSocket = filepath.Join(dir, "none.sock") // no daemon: state files
	t.Cleanup(func() { env.BaseStateDir, env.DaemonSocket = prevState, prevSock })

	native, vm = startStopTestProc(t), startStopTestProc(t)
	for _, c := range []*config.Config{
		{Name: "rr-native", Startup: true, Background: true, Status: "running",
			ContPid: native.pid, ContPidStart: native.start},
		{Name: "rr-vm", VM: "kvm", Startup: true, Background: true, Status: "running",
			HostPid: vm.pid, HostPidStart: vm.start},
	} {
		c.RootfsDir = filepath.Join(dir, "rootfs", c.Name)
		c.ChangeDir = filepath.Join(dir, "changedir", c.Name)
		if err := controller.SetContainer(c); err != nil {
			t.Fatal(err)
		}
	}
	return native, vm
}

// A daemon stop (OpenRC/systemd send SIGTERM; a restart or an upgrade is a
// stop then a start) must leave a detached container running: the next
// daemon adopts it. Only VMs, which depend on the daemon, are stopped.
func TestSignalProxyLeavesDetachedContainersRunning(t *testing.T) {
	native, vm := useStopTestState(t)

	// Keep SIGTERM from taking its default action before signalProxy has
	// registered for it.
	hold := make(chan os.Signal, 1)
	signal.Notify(hold, syscall.SIGTERM)
	defer signal.Stop(hold)

	stop := make(chan bool, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go signalProxy(stop, &wg)
	deadline := time.After(10 * time.Second)
	for stopped := false; !stopped; {
		syscall.Kill(os.Getpid(), syscall.SIGTERM)
		select {
		case <-stop:
			stopped = true
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatal("signalProxy did not request the daemon stop")
		}
	}
	wg.Wait()

	if native.exitsWithin(time.Second) {
		t.Error("the daemon stop killed a detached native container")
	}
	if !vm.exitsWithin(5 * time.Second) {
		t.Error("the daemon stop left a startup VM running")
	}
}

func TestCheckZombieLeavesDetachedContainersRunning(t *testing.T) {
	native, vm := useStopTestState(t)

	checkZombie()

	if native.exitsWithin(time.Second) {
		t.Error("checkZombie killed a detached native container")
	}
	if !vm.exitsWithin(5 * time.Second) {
		t.Error("checkZombie left a startup VM running")
	}
}
