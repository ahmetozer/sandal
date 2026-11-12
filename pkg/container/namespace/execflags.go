package namespace

import (
	"flag"
	"fmt"
)

// ExecKinds lists the namespace kinds `sandal exec` can join or keep on the
// host. The user namespace is missing on purpose: the kernel refuses
// setns(CLONE_NEWUSER) from a multi-threaded process, and every Go process
// is one, so exec never joins it and the command keeps the caller's
// credentials.
var ExecKinds = []Name{"cgroup", "ipc", "mnt", "net", "pid", "uts"}

// ExecFlags registers -ns-<kind> for every kind in ExecKinds on f. The
// returned function, called after f.Parse, gives the non-empty values by
// kind ("net" -> "cont:db"). Kinds left unset are missing from the map: exec
// joins the container's own namespace for them.
//
// The flags are plain strings here; namespace.ForExec validates and
// resolves them on the side that joins (the host for native containers, the
// guest for VM containers).
func ExecFlags(f *flag.FlagSet) func() map[string]string {
	values := make(map[Name]*string, len(ExecKinds))
	for _, kind := range ExecKinds {
		v := new(string)
		values[kind] = v
		f.StringVar(v, "ns-"+string(kind), "", fmt.Sprintf(
			"%s namespace to use instead of the container's: host, <pid>, pid:<pid>, file:<path> or cont:<name>", kind))
	}
	return func() map[string]string {
		var set map[string]string
		for _, kind := range ExecKinds {
			if v := *values[kind]; v != "" {
				if set == nil {
					set = make(map[string]string)
				}
				set[string(kind)] = v
			}
		}
		return set
	}
}
