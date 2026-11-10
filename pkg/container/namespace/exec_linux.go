//go:build linux

package namespace

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// ForExec builds the namespace set `sandal exec` joins for a container
// whose init runs as contPid. own is the container's stored namespace
// configuration and overrides holds the -ns-<kind> values by kind (see
// ExecFlags).
//
// For every kind in ExecKinds:
//
//   - an override "host" keeps the caller's namespace;
//   - any other override (<pid>, pid:<pid>, file:<path>, cont:<name>) is
//     joined; cont: targets still have to go through Resolve;
//   - without an override, a kind the container shares with the host stays
//     on the host, and every other kind is joined through
//     /proc/<contPid>/ns/<kind>. That is the container's own namespace even
//     when it was borrowed at run time (-ns-net cont:B): the init is in it
//     by definition, while the target stored back then can be stale.
//
// The user namespace is never joined (see ExecKinds). An empty override is
// the same as no override.
//
// It fails for an unknown kind, a user namespace target, a file: target for
// mnt (there is no process root to enter) and a target with bad syntax. The
// returned map and its values are new, so callers may Resolve it without
// touching own.
func ForExec(contPid int, own Namespaces, overrides map[string]string) (Namespaces, error) {
	if contPid <= 0 {
		return nil, fmt.Errorf("invalid container pid %d", contPid)
	}

	kinds := make([]string, 0, len(overrides))
	for kind := range overrides {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		v := overrides[kind]
		if kind == "user" {
			if v == "" || v == "host" {
				continue
			}
			return nil, fmt.Errorf("-ns-user %q: exec never joins a user namespace; the command keeps the caller's credentials", v)
		}
		if !slices.Contains(ExecKinds, Name(kind)) {
			return nil, fmt.Errorf("unknown namespace kind %q: use one of %v", kind, ExecKinds)
		}
		if v == "" || v == "host" {
			continue
		}
		if err := validateTargetSyntax(v); err != nil {
			return nil, fmt.Errorf("-ns-%s %q: %w", kind, v, err)
		}
		if kind == "mnt" && strings.HasPrefix(v, "file:") {
			return nil, fmt.Errorf("-ns-mnt %q: file: targets are not supported for the mount namespace, use host, <pid>, pid:<pid> or cont:<name>", v)
		}
	}

	join := make(Namespaces, len(ExecKinds))
	for _, kind := range ExecKinds {
		v := overrides[string(kind)]
		switch {
		case v == "host":
			join[kind] = hostConf()
		case v != "":
			join[kind] = targetConf(v)
		case own.Get(kind).IsHost:
			join[kind] = hostConf()
		default:
			join[kind] = targetConf(fmt.Sprintf("pid:%d", contPid))
		}
	}
	return join, nil
}

func hostConf() NamespaceConf {
	v := "host"
	return NamespaceConf{UserValue: &v, IsHost: true}
}

func targetConf(target string) NamespaceConf {
	return NamespaceConf{UserValue: &target, IsUserDefined: true}
}
