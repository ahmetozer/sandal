package capabilities

import (
	"flag"
)

func ParseFlagSet(f *flag.FlagSet, cap *Capabilities) {
	f.Var(&cap.AddCapabilities, "cap-add", "add capabilities to container")
	f.Var(&cap.DropCapabilities, "cap-drop", "drop capabilities from container")
	f.BoolVar(&cap.Privileged, "privileged", false, "give extended privileges to container")
}
