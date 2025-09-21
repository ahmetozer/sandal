package namespace

func (ns *NamespaceConf) defaults() {
	switch *ns.UserValue {
	case "host":
		ns.IsHost = true
		ns.IsUserDefined = false
	case "":
		ns.IsUserDefined = false
	default:
		ns.IsUserDefined = true
	}
}

func (Ns *Namespaces) defaults() {
	// If value is not host and empty, allocate an namespace
	for name := range namespaceList {
		conf, exist := (*Ns)[name]
		if !exist {
			conf = NamespaceConf{}
		}
		conf.defaults()
		(*Ns)[name] = conf
	}
}

func (Ns *Namespaces) Defaults() error {
	Ns.defaults()
	// for name, nsConf := range *Ns {
	// 	(*Ns)[name] = nsConf
	// }
	return nil
}
