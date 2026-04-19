//go:build darwin

package capabilities

import "flag"

// ParseFlagSet on macOS registers the same capability flags as Linux so that
// flag.Parse accepts them. The actual enforcement happens inside the VM guest
// where the Linux kernel applies capabilities natively.
func ParseFlagSet(f *flag.FlagSet, cap *Capabilities) {
	f.Var(&cap.AddCapabilities, "cap-add", "add capabilities to container")
	f.Var(&cap.DropCapabilities, "cap-drop", "drop capabilities from container")
	f.BoolVar(&cap.Privileged, "privileged", false, "give extended privileges to container")
}
