//go:build linux

package sandal

import (
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
)

type vmKillStubs struct {
	mu      sync.Mutex
	signals []int
	reaps   int
}

func (s *vmKillStubs) sigs() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.signals...)
}

func (s *vmKillStubs) reapCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reaps
}

// stubVMKill replaces the kill seams. mgmt is the result of the management
// socket kill; forward makes sendSig deliver the signal for real.
func stubVMKill(t *testing.T, mgmt func() error, forward bool) *vmKillStubs {
	t.Helper()
	s := &vmKillStubs{}
	oldMgmt, oldSig, oldRunning, oldReap, oldPoll := killViaMgmtFn, sendSig, pidRunningAs, reapVMState, vmPollInterval
	t.Cleanup(func() {
		killViaMgmtFn, sendSig, pidRunningAs, reapVMState, vmPollInterval = oldMgmt, oldSig, oldRunning, oldReap, oldPoll
	})
	killViaMgmtFn = func(string, int) error { return mgmt() }
	sendSig = func(pid, sig int) error {
		s.mu.Lock()
		s.signals = append(s.signals, sig)
		s.mu.Unlock()
		if forward {
			return crt.SendSig(pid, sig)
		}
		return nil
	}
	pidRunningAs = crt.IsPidRunningAs
	reapVMState = func(c *config.Config) error {
		s.mu.Lock()
		s.reaps++
		s.mu.Unlock()
		c.Status = "killed"
		c.HostPid = 0
		c.HostPidStart = 0
		return nil
	}
	vmPollInterval = 10 * time.Millisecond
	return s
}

// startVMM starts a stand-in VMM process and returns a VM config for it.
func startVMM(t *testing.T) (*config.Config, *exec.Cmd) {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-done
	})
	start, err := crt.ProcessStartTime(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("start time: %v", err)
	}
	return &config.Config{Name: "vm-test", VM: "kvm", HostPid: cmd.Process.Pid, HostPidStart: start, Status: "running"}, cmd
}

func TestKillVMMgmtWaitsForExit(t *testing.T) {
	c, cmd := startVMM(t)
	s := stubVMKill(t, func() error {
		// Simulated guest poweroff.
		time.AfterFunc(100*time.Millisecond, func() { cmd.Process.Signal(syscall.SIGTERM) })
		return nil
	}, false)

	if err := killVM(c, 15, 2); err != nil {
		t.Fatalf("killVM: %v", err)
	}
	if got := s.reapCount(); got != 1 {
		t.Errorf("reaps = %d, want 1", got)
	}
	if got := s.sigs(); len(got) != 0 {
		t.Errorf("signals = %v, want none", got)
	}
}

func TestKillVMMgmtTimeout(t *testing.T) {
	c, _ := startVMM(t)
	pid := c.HostPid
	s := stubVMKill(t, func() error { return nil }, false)

	if err := killVM(c, 15, 1); err == nil {
		t.Fatal("killVM: want timeout error")
	}
	if got := s.reapCount(); got != 0 {
		t.Errorf("reaps = %d, want 0", got)
	}
	if c.HostPid != pid {
		t.Errorf("HostPid = %d, want %d", c.HostPid, pid)
	}
}

func TestKillVMMgmtNoWait(t *testing.T) {
	c, _ := startVMM(t)
	s := stubVMKill(t, func() error { return nil }, false)

	begin := time.Now()
	if err := killVM(c, 15, 0); err != nil {
		t.Fatalf("killVM: %v", err)
	}
	if d := time.Since(begin); d > 500*time.Millisecond {
		t.Errorf("killVM took %v, want immediate return", d)
	}
	if got := s.reapCount(); got != 0 {
		t.Errorf("reaps = %d, want 0", got)
	}
}

func TestKillVMMgmtFailFallsBackToSIGKILL(t *testing.T) {
	c, _ := startVMM(t)
	s := stubVMKill(t, func() error { return errors.New("no socket") }, true)

	if err := killVM(c, 15, 2); err != nil {
		t.Fatalf("killVM: %v", err)
	}
	if got := s.sigs(); len(got) != 1 || got[0] != 9 {
		t.Errorf("signals = %v, want [9]", got)
	}
	if got := s.reapCount(); got != 1 {
		t.Errorf("reaps = %d, want 1", got)
	}
}

func TestKillVMHostRecycledPid(t *testing.T) {
	c, _ := startVMM(t)
	c.HostPidStart++
	s := stubVMKill(t, func() error { return nil }, true)

	if err := killVM(c, 9, 2); err != nil {
		t.Fatalf("killVM: %v", err)
	}
	if got := s.sigs(); len(got) != 0 {
		t.Errorf("signals = %v, want none", got)
	}
	if got := s.reapCount(); got != 1 {
		t.Errorf("reaps = %d, want 1", got)
	}
}
