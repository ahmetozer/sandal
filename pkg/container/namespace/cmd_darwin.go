//go:build darwin

package namespace

import (
	"flag"
	"fmt"
)

// namespaceNames lists the same namespace names as the Linux namespaceList so
// that the CLI accepts these flags on macOS. The actual namespace isolation
// happens inside the VM guest where the Linux kernel handles them natively.
var namespaceNames = []Name{"mnt", "ipc", "cgroup", "pid", "net", "user", "uts"}

// ParseFlagSet registers namespace flags on macOS so that flag.Parse accepts
// them. The parsed values are stored in the returned Namespaces map and
// forwarded to the VM guest via HostArgs.
func ParseFlagSet(f *flag.FlagSet) (NS Namespaces) {
	NS = make(Namespaces, len(namespaceNames))

	for _, namespace := range namespaceNames {
		var (
			defaultNamespace string
			userValue        string
		)

		switch namespace {
		case "user":
			defaultNamespace = "host"
		default:
			defaultNamespace = ""
		}

		NS[namespace] = NamespaceConf{UserValue: &userValue}

		f.StringVar(&userValue, fmt.Sprintf("ns-%s", namespace), defaultNamespace, fmt.Sprintf("%s namespace or host", namespace))
	}

	return
}
