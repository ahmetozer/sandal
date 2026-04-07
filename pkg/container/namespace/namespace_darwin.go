//go:build darwin

package namespace

// Cloneflags is not applicable on macOS.
func (NS Namespaces) Cloneflags() uintptr { return 0 }

// String returns the user value of this namespace config.
func (nsConf NamespaceConf) String() (namespaceValue string) {
	if nsConf.UserValue != nil {
		namespaceValue = *nsConf.UserValue
	}
	return
}

// Get returns the namespace config for the given name.
func (NS Namespaces) Get(name Name) NamespaceConf {
	if NS != nil {
		if conf, ok := NS[name]; ok {
			return conf
		}
	}
	return NamespaceConf{}
}
