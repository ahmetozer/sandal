package capabilities

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func (cap *Capabilities) Set() (err error) {

	// Get current capability sets
	var header unix.CapUserHeader
	var data [2]unix.CapUserData

	header.Version = unix.LINUX_CAPABILITY_VERSION_3
	header.Pid = 0 // 0 means current process

	if err := unix.Capget(&header, &data[0]); err != nil {
		return fmt.Errorf("failed to get capabilities: %w", err)
	}

	// Build the capability set
	var capSet map[string]bool

	if cap.Privileged {
		// For privileged mode, start with ALL capabilities
		capSet = make(map[string]bool)
		for capName := range capabilityMap {
			capSet[capName] = true
		}
		// Still allow dropping specific capabilities in privileged mode
		for _, c := range cap.DropCapabilities {
			delete(capSet, string(c))
		}
	} else {

		capSet = make(map[string]bool)
		for _, c := range defaultCaps {
			capSet[c] = true
		}

		// Add requested capabilities
		for _, c := range cap.AddCapabilities {
			capSet[string(c)] = true
		}

		// Drop requested capabilities
		for _, c := range cap.DropCapabilities {
			delete(capSet, string(c))
		}
	}

	// First, drop capabilities from the bounding set
	// This prevents them from being gained during exec
	for capName, capNum := range capabilityMap {
		if !capSet[capName] {
			// Drop this capability from the bounding set
			if _, _, errno := unix.Syscall(unix.SYS_PRCTL, unix.PR_CAPBSET_DROP, uintptr(capNum), 0); errno != 0 {
				return fmt.Errorf("failed to drop capability %s from bounding set: %v", capName, errno)
			}
		}
	}

	// Convert capability names to bitmasks
	// Reset all capabilities to 0 first
	data[0].Effective = 0
	data[0].Permitted = 0
	data[0].Inheritable = 0
	data[1].Effective = 0
	data[1].Permitted = 0
	data[1].Inheritable = 0

	for capName := range capSet {
		capNum, ok := capabilityMap[capName]
		if !ok {
			return fmt.Errorf("unknown capability: %s", capName)
		}
		if capNum < 32 {
			// For capabilities 0-31, use data[0]
			data[0].Effective |= 1 << capNum
			data[0].Permitted |= 1 << capNum
			data[0].Inheritable |= 1 << capNum
		} else {
			// For capabilities 32-63, use data[1]
			data[1].Effective |= 1 << (capNum - 32)
			data[1].Permitted |= 1 << (capNum - 32)
			data[1].Inheritable |= 1 << (capNum - 32)
		}
	}

	if err := unix.Capset(&header, &data[0]); err != nil {
		return fmt.Errorf("failed to set capabilities: %w", err)
	}

	return nil
}

// RestoreEffective restores the effective capabilities from the permitted set
// This should be called after switching from root to a non-root user, as the
// kernel clears the effective set during that transition (even with KEEPCAPS).
func (cap *Capabilities) RestoreEffective() error {
	// Get current capability sets
	var header unix.CapUserHeader
	var data [2]unix.CapUserData

	header.Version = unix.LINUX_CAPABILITY_VERSION_3
	header.Pid = 0 // 0 means current process

	if err := unix.Capget(&header, &data[0]); err != nil {
		return fmt.Errorf("failed to get capabilities: %w", err)
	}

	// Copy permitted capabilities to effective
	// The permitted set was preserved by PR_SET_KEEPCAPS during user switch
	data[0].Effective = data[0].Permitted
	data[1].Effective = data[1].Permitted

	if err := unix.Capset(&header, &data[0]); err != nil {
		return fmt.Errorf("failed to restore effective capabilities: %w", err)
	}

	// Set ambient capabilities so they're preserved across execve()
	// This is required for non-root processes to keep capabilities after exec
	// Build the capability set again to determine which caps to set as ambient
	var capSet map[string]bool

	if cap.Privileged {
		// For privileged mode, set ALL capabilities as ambient
		capSet = make(map[string]bool)
		for capName := range capabilityMap {
			capSet[capName] = true
		}
		// Still allow dropping specific capabilities in privileged mode
		for _, c := range cap.DropCapabilities {
			delete(capSet, string(c))
		}
	} else {

		capSet = make(map[string]bool)
		for _, c := range defaultCaps {
			capSet[c] = true
		}

		for _, c := range cap.AddCapabilities {
			capSet[string(c)] = true
		}

		for _, c := range cap.DropCapabilities {
			delete(capSet, string(c))
		}
	}

	// Set each capability in the ambient set
	// PR_CAP_AMBIENT = 47, PR_CAP_AMBIENT_RAISE = 2
	for capName := range capSet {
		capNum, ok := capabilityMap[capName]
		if !ok {
			continue
		}
		// PR_CAP_AMBIENT_RAISE: Add capability to ambient set
		if _, _, errno := unix.Syscall6(unix.SYS_PRCTL, 47, 2, uintptr(capNum), 0, 0, 0); errno != 0 {
			// Don't fail if ambient caps aren't supported (older kernels)
			fmt.Printf("Warning: failed to set ambient capability %s: %v\n", capName, errno)
		}
	}

	return nil
}
