package namespace

import (
	"fmt"
	"strconv"
)

func (ns *NamespaceConf) defaults() {

	switch *ns.UserValue {
	case "host":
		ns.host = true
		ns.userDefined = false
	case "":
		ns.userDefined = false
	default:
		ns.userDefined = true
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
func (Ns *Namespaces) AllocateNS() error {

	// Ns.load()

	var err error
	// Allocate default namespaces
	Ns.defaults()

	for name, nsConf := range *Ns {
		if nsConf.userDefined {
			nsConf.Pid, err = strconv.Atoi(*nsConf.UserValue)
			if err != nil {
				return fmt.Errorf("allocateNS pid: %s", err)
			}
		}
		(*Ns)[name] = nsConf
	}

	return nil
}
