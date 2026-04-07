//go:build darwin

package guest

// IsVMInit returns false on macOS — the macOS host is never a VM init process.
func IsVMInit() bool { return false }
