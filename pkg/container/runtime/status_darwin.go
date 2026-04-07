//go:build darwin

package runtime

// isPidAlive on macOS simply returns true — no /proc zombie check available.
func isPidAlive(pid int) (bool, error) {
	return true, nil
}
