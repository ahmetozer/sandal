//go:build darwin

package namespace

import "flag"

// ParseFlagSet on macOS returns empty Namespaces — namespace flags are
// not applicable on the host (they're handled inside the VM).
func ParseFlagSet(f *flag.FlagSet) Namespaces {
	return make(Namespaces)
}
