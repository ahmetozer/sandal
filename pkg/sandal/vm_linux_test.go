//go:build linux

package sandal

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/console"
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
)

func TestSetForkedVMExit(t *testing.T) {
	const pid = 4242
	exitErr := errors.New("exit status 1")
	tests := []struct {
		name       string
		latest     config.Config
		err        error
		wantUpdate bool
		wantStatus string
	}{
		{"vmm exited cleanly without recording", config.Config{HostPid: pid, Status: "running"}, nil, true, "exit 0"},
		{"vmm failed without recording", config.Config{HostPid: pid, Status: "running"}, exitErr, true, "err exit status 1"},
		{"vmm recorded the panic", config.Config{HostPid: pid, Status: "err guest kernel panicked or requested reboot"}, exitErr, false, "err guest kernel panicked or requested reboot"},
		{"vmm recorded a clean exit", config.Config{HostPid: pid, Status: "exit 0"}, nil, false, "exit 0"},
		{"stopped by the user", config.Config{HostPid: pid, Status: "stop"}, exitErr, false, "stop"},
		{"record belongs to a newer run", config.Config{HostPid: pid + 1, Status: "running"}, exitErr, false, "running"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.latest
			if got := setForkedVMExit(&c, pid, tt.err); got != tt.wantUpdate || c.Status != tt.wantStatus {
				t.Errorf("setForkedVMExit() = %v, status %q; want %v, %q", got, c.Status, tt.wantUpdate, tt.wantStatus)
			}
		})
	}
}

// startFakeVMM starts a process that never reports ready, like a stuck
// VMM, and returns it with a channel closed once it has been reaped.
func startFakeVMM(t *testing.T) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-exited
	})
	return cmd, exited
}

// `run -vm -d` must not wait forever for a VMM that never reports ready:
// it fails naming the VM and its console, and stops the VMM instead of
// leaving it running unreported.
func TestAwaitVMReadyTimeout(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close() // held open: the VMM neither reports nor exits
	cmd, exited := startFakeVMM(t)

	start := time.Now()
	err = awaitVMReady(r, cmd.Process, exited, "ff-vm", 200*time.Millisecond)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("awaitVMReady took %v with a 200ms timeout", took)
	}
	if err == nil {
		t.Fatal("awaitVMReady = nil for a VMM that never reported ready")
	}
	for _, want := range []string{"ff-vm", console.StdoutPath("ff-vm")} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
	select {
	case <-exited:
	default:
		t.Error("the VMM is still running after the timeout")
	}
}

// A VMM that reports ready in time is left running.
func TestAwaitVMReadyInTime(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cmd, exited := startFakeVMM(t)
	vmconfig.ReportReady(w, nil)

	if err := awaitVMReady(r, cmd.Process, exited, "ff-vm", 5*time.Second); err != nil {
		t.Fatalf("awaitVMReady = %v, want nil", err)
	}
	select {
	case <-exited:
		t.Error("a ready VMM was stopped")
	default:
	}
}
