//go:build darwin

package capabilities

import "flag"

// ParseFlagSet on macOS is a no-op — capability flags are handled inside the VM.
func ParseFlagSet(f *flag.FlagSet, cap *Capabilities) {}
