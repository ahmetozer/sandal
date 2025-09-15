package namespace

import (
	"flag"
	"fmt"
)

func ParseFlagSet(f *flag.FlagSet) (NS Namespaces) {
	NS = make(Namespaces, len(namespaceList))

	for namespace := range namespaceList {
		defaultValue := ""
		if namespace == "user" {
			defaultValue = "host"
		}
		userValue := ""
		NS[namespace] = NamespaceConf{UserValue: &userValue}
		f.StringVar(&userValue, "ns-"+namespace, defaultValue, fmt.Sprintf("%s namespace or host", namespace))
	}
	return
}
