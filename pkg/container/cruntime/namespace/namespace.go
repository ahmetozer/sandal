package namespace

import (
	"syscall"
)

type NamespaceConf struct {
	UserValue     *string
	IsUserDefined bool
	IsHost        bool
}

type Name string

type Namespaces map[Name]NamespaceConf

func (NS Namespaces) Cloneflags() uintptr {
	var Cloneflags uintptr
	for name, conf := range NS {
		if conf.IsHost {
			continue
		}
		Cloneflags |= namespaceList[name]
	}
	return Cloneflags
}

var namespaceList = map[Name]uintptr{
	"mnt":    syscall.CLONE_NEWNS,
	"ipc":    syscall.CLONE_NEWIPC,
	"cgroup": syscall.CLONE_NEWCGROUP,
	"pid":    syscall.CLONE_NEWPID,
	"net":    syscall.CLONE_NEWNET,
	"user":   syscall.CLONE_NEWUSER, // Default is host
	"uts":    syscall.CLONE_NEWUTS,
	// "time":   syscall.CLONE_NEWTIME,

}

func (nsConf NamespaceConf) String() (namespaceValue string) {
	if nsConf.UserValue != nil {
		namespaceValue = *nsConf.UserValue
	}
	return
}

func (NS Namespaces) Get(name Name) NamespaceConf {
	if NS != nil {
		_, k := NS[name]
		if !k {
			panic("unexpected namespace is called")
		}
	}
	return NS[name]
}

// func toString(clone uintptr) (name string) {
// 	var val uintptr
// 	for name, val = range namespaceList {
// 		if clone == val {
// 			return
// 		}
// 	}
// 	return
// }
