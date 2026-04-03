//go:build darwin

package namespace

// Defaults is a no-op on macOS — namespaces are handled inside the VM.
func (Ns *Namespaces) Defaults() error {
	return nil
}
