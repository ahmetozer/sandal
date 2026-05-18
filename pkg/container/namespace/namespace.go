//go:build linux

package namespace

import (
	"fmt"
	"syscall"
)

// Cloneflags returns the CLONE_NEW* bitmask of namespaces that should be
// freshly created. Entries marked IsHost (share the host) or IsUserDefined
// (join an existing target via SetNS) are skipped — CLONE_NEW* semantically
// means "make a new one", which is the opposite of joining.
//
// Note: setns(CLONE_NEWNS) requires the calling thread's fs_struct to be
// private (fs->users == 1). Callers that combine this bitmask with a later
// setns into a user-defined mnt namespace (e.g. Enter) must also unshare
// CLONE_NEWNS explicitly — see enter_linux.go.
func (NS Namespaces) Cloneflags() uintptr {
	var Cloneflags uintptr
	for name, conf := range NS {
		if conf.IsHost || conf.IsUserDefined {
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
