//go:build linux

package namespace

import (
	"fmt"
	"strconv"
	"strings"
)

// Validate checks for namespace target combinations that sandal cannot
// safely handle on the create path (`sandal run`). Call after Defaults().
//
// Unsupported on `sandal run`:
//
//   - --ns-pid <target>: setns(CLONE_NEWPID) only updates the calling
//     task's pid_ns_for_children, not its own pidns. unix.Exec then
//     preserves the calling task's pidns, so the user's process would
//     still run in the host pidns — contrary to the flag's intent.
//     Implementing this requires an extra fork in the child.
//
//   - --ns-mnt <target>: sandal mounts an overlay rootfs in the fresh
//     mntns the child clone() creates. Joining a foreign mntns would
//     silently discard that rootfs setup. Until the semantics are
//     defined, reject rather than misbehave.
//
// Joining via Enter() (used by `sandal exec` and the netns dialer)
// supports all targets; this validator only restricts `sandal run`.
func (NS Namespaces) Validate() error {
	if mnt, ok := NS["mnt"]; ok && mnt.IsUserDefined {
		return fmt.Errorf("--ns-mnt only accepts 'host' or empty (new) on `sandal run`; got %q — join targets conflict with overlay rootfs setup", mnt.String())
	}
	if pid, ok := NS["pid"]; ok && pid.IsUserDefined {
		return fmt.Errorf("--ns-pid only accepts 'host' or empty (new) on `sandal run`; got %q — joining a pidns requires an extra fork in the child that is not yet implemented", pid.String())
	}
	for name, conf := range NS {
		if !conf.IsUserDefined {
			continue
		}
		if err := validateTargetSyntax(conf.String()); err != nil {
			return fmt.Errorf("--ns-%s %q: %w", name, conf.String(), err)
		}
	}
	return nil
}

// validateTargetSyntax accepts the three forms documented in set.go:
//
//   - "<pid>"      bare positive integer
//   - "pid:<pid>"  positive integer after the "pid:" prefix
//   - "file:<path>" non-empty path after the "file:" prefix
//
// Anything else is rejected so the failure surfaces at parse time on the
// host, not inside the sandal-child after cmd.Start().
func validateTargetSyntax(v string) error {
	if v == "" {
		return fmt.Errorf("empty target")
	}
	if strings.HasPrefix(v, "file:") {
		if len(v) <= len("file:") {
			return fmt.Errorf("file: target requires a path")
		}
		return nil
	}
	num := v
	if strings.HasPrefix(v, "pid:") {
		num = v[len("pid:"):]
	}
	pid, err := strconv.Atoi(num)
	if err != nil || pid <= 0 {
		return fmt.Errorf("target must be <pid>, pid:<pid>, or file:<path>")
	}
	return nil
}
