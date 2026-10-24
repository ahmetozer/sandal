//go:build linux && (amd64 || arm64)

package kvm

import "errors"

var (
	// ErrGuestReset means the guest asked to reboot. There is no in-place
	// reboot, so the VM stops. With panic=-1 on the cmdline a guest kernel
	// panic ends this way (PSCI SYSTEM_RESET on arm64, triple fault on amd64).
	ErrGuestReset = errors.New("guest kernel panicked or requested reboot")
	// ErrGuestCrash means the guest reported a crash (KVM_SYSTEM_EVENT_CRASH).
	ErrGuestCrash = errors.New("guest kernel crashed")
)

// guestStopError reports whether a vCPU exit means the guest has stopped, and
// why: nil for a clean poweroff, an error for a reset or crash. eventType is
// only read for KVM_EXIT_SYSTEM_EVENT.
func guestStopError(exitReason, eventType uint32) (bool, error) {
	switch exitReason {
	case kvmExitHLT:
		return true, nil
	case kvmExitShutdown:
		// Triple fault. On amd64 this is where the kernel's reboot
		// fallbacks end, as the VMM has no ACPI or keyboard controller reset.
		return true, ErrGuestReset
	case kvmExitSystemEvent:
		switch eventType {
		case kvmSystemEventShutdown:
			return true, nil
		case kvmSystemEventReset:
			return true, ErrGuestReset
		case kvmSystemEventCrash:
			return true, ErrGuestCrash
		}
	}
	return false, nil
}
