package namespace

import (
	"flag"
	"fmt"
)

func ParseFlagSet(f *flag.FlagSet) (NS Namespaces) {
	NS = make(Namespaces, len(namespaceList))

	for namespace := range namespaceList {
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
