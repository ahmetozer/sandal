package namespace

import (
	"syscall"
)

type NamespaceConf struct {
	UserValue   *string
	SystemValue string
	Pid         int
	userDefined bool
	host        bool
}

type Namespaces map[string]NamespaceConf

func (NS Namespaces) Cloneflags() uintptr {
	var Cloneflags uintptr
	for name, conf := range NS {
		if conf.host {
			continue
		}
		Cloneflags |= namespaceList[name]
	}
	return Cloneflags
}

var namespaceList = map[string]uintptr{
	"mnt":    syscall.CLONE_NEWNS,
	"ipc":    syscall.CLONE_NEWIPC,
	"time":   syscall.CLONE_NEWTIME,
	"cgroup": syscall.CLONE_NEWCGROUP,
	"pid":    syscall.CLONE_NEWPID,
	"net":    syscall.CLONE_NEWNET,
	"user":   syscall.CLONE_NEWUSER,
	"uts":    syscall.CLONE_NEWUTS,
}
