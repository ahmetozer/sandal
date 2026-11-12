package runtime

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

// ContPidLookup returns a lookup for namespace.Resolve that maps a sandal
// container name to the pid of its running init, searching conts. It is the
// one place that resolves -ns-<kind> cont:<name> targets, for `sandal run`
// and `sandal exec` alike.
//
// The lookup fails when the container is missing, runs in a VM (its
// namespaces live in the guest kernel, not this one), or is not running.
// "Not running" includes a recorded pid that now belongs to an unrelated
// process (start-time mismatch), so a stale record is never joined.
func ContPidLookup(conts []*config.Config) func(name string) (int, error) {
	return func(name string) (int, error) {
		for _, c := range conts {
			if c.Name != name {
				continue
			}
			if c.VM != "" {
				return 0, fmt.Errorf("container %q runs in a VM; its namespaces are not reachable from here", name)
			}
			if c.ContPid <= 0 {
				return 0, fmt.Errorf("container %q is not running", name)
			}
			alive, err := IsPidRunningAs(c.ContPid, c.ContPidStart)
			if err != nil {
				return 0, fmt.Errorf("container %q: check pid %d: %w", name, c.ContPid, err)
			}
			if !alive {
				return 0, fmt.Errorf("container %q is not running", name)
			}
			return c.ContPid, nil
		}
		return 0, fmt.Errorf("container %q not found", name)
	}
}
