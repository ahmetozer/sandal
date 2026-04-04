//go:build linux

package namespace

import (
	"fmt"
	"syscall"
)

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

// DefaultsForPid builds a Namespaces map targeting all standard namespaces
// for the given PID. Used when c.NS is empty (e.g., VM containers where
// namespace flags weren't parsed on the host).
func DefaultsForPid(pid int) Namespaces {
	ns := make(Namespaces, len(namespaceList))
	for name := range namespaceList {
		// Skip user namespace (default is host) and time namespace
		// (can't setns on multithreaded process).
		if name == "user" || name == "time" {
			continue
		}
		val := fmt.Sprintf("pid:%d", pid)
		ns[name] = NamespaceConf{
			UserValue:     &val,
			IsUserDefined: true,
		}
	}
	return ns
}
