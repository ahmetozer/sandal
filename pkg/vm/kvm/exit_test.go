//go:build linux && (amd64 || arm64)

package kvm

import (
	"errors"
	"testing"
)

func TestGuestStopError(t *testing.T) {
	tests := []struct {
		name     string
		reason   uint32
		event    uint32
		wantStop bool
		wantErr  error
	}{
		{"psci system off is a clean poweroff", kvmExitSystemEvent, kvmSystemEventShutdown, true, nil},
		{"psci system reset ends the VM with an error", kvmExitSystemEvent, kvmSystemEventReset, true, ErrGuestReset},
		{"guest crash event ends the VM with an error", kvmExitSystemEvent, kvmSystemEventCrash, true, ErrGuestCrash},
		{"triple fault ends the VM with an error", kvmExitShutdown, 0, true, ErrGuestReset},
		{"hlt exit stops cleanly", kvmExitHLT, 0, true, nil},
		{"other system events keep running", kvmExitSystemEvent, 4, false, nil},
		{"io exits keep running", kvmExitIO, 0, false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stop, err := guestStopError(tt.reason, tt.event)
			if stop != tt.wantStop || !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Errorf("guestStopError(%d, %d) = (%v, %v), want (%v, %v)", tt.reason, tt.event, stop, err, tt.wantStop, tt.wantErr)
			}
		})
	}
}

func TestStopErrorReachesWaiter(t *testing.T) {
	vm := &VM{state: VMStateRunning, stopCh: make(chan struct{}), stoppedCh: make(chan error, 1)}
	vm.stop(ErrGuestReset)
	vm.Stop() // a later host stop must not clear the guest's reason
	if vm.stopReason() != ErrGuestReset {
		t.Fatalf("stop reason = %v, want %v", vm.stopReason(), ErrGuestReset)
	}

	clean := &VM{state: VMStateRunning, stopCh: make(chan struct{}), stoppedCh: make(chan error, 1)}
	clean.Stop()
	if err := clean.stopReason(); err != nil {
		t.Fatalf("host stop reason = %v, want nil", err)
	}
}
