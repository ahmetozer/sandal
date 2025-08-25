package cruntime

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"golang.org/x/sys/unix"
)

func loadNamespaceIDs(c *config.Config) {
	for _, ns := range config.Namespaces {
		if c.NS[ns].Value == "host" {
			continue
		}
		if c.NS[ns].Custom == nil {
			c.NS[ns].Custom = make(map[string]interface{})
		}
		if c.NS[ns].Value != "" {
			c.NS[ns].Custom = make(map[string]interface{})
			c.NS[ns].Custom["changed"] = true
		}
		c.NS[ns].Custom["value"] = readNamespace(fmt.Sprintf("/proc/%d/ns/%s", c.ContPid, ns))
	}
}

func readNamespace(f string) string {
	s, err := os.Readlink(f)
	if err != nil {
		return ""
	}
	return parseNamspaceInfo(s)
}

func parseNamspaceInfo(s string) string {
	ns := strings.Split(s, "[")
	if ns == nil {
		return s
	}
	if len(ns) == 2 {
		return strings.Trim(ns[1], "]")
	}
	return s
}

func AttachContainerToPID(c *config.Config, masterPid int) error {
	if err := syscall.Setpgid(c.ContPid, masterPid); err != nil {
		return fmt.Errorf("error setting %d process group id: %s", c.ContPid, err)
	}
	if pgid, err := syscall.Getpgid(c.ContPid); err != nil || pgid != masterPid {
		return fmt.Errorf("container group pid is not verified: %s", err)
	}
	return nil
}

func SetNs(nsname string, pid, nstype int) error {
	path := fmt.Sprintf("/proc/%d/ns/%s", pid, nsname)
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open namespace file %s: %v", path, err)
	}

	// Set the namespace
	if err := unix.Setns(int(file.Fd()), nstype); err != nil {
		return fmt.Errorf("failed to set namespace %s: %v", nsname, err)
	}
	return nil
}

type NsConf struct {
	Nsname    string
	CloneFlag uintptr
	Pid       int
}

type Namespaces struct {
	Cloneflags uintptr
	NsConfs    []NsConf
}

func checkNewNsRequired(val string) bool {
	return val != "host" && val == ""
}

func toSyscall(s string) (i uintptr, e error) {
	switch s {
	case "mnt":
		i = syscall.CLONE_NEWNS
	case "ipc":
		i = syscall.CLONE_NEWIPC
	case "time":
		i = syscall.CLONE_NEWTIME
	case "cgroup":
		i = syscall.CLONE_NEWCGROUP
	case "pid":
		i = syscall.CLONE_NEWPID
	case "net":
		i = syscall.CLONE_NEWNET
	case "user":
		i = syscall.CLONE_NEWUSER
	case "uts":
		i = syscall.CLONE_NEWUTS
	default:
		e = fmt.Errorf("unknown namespace: %s", s)
	}
	return
}

func (ns *Namespaces) allocateNs(c *config.Config, name string) error {
	clone, err := toSyscall(name)
	if err != nil {
		return err
	}
	if checkNewNsRequired(c.NS[name].Value) {
		ns.Cloneflags |= clone
	} else if c.NS[name].Value != "host" {
		i, err := strconv.Atoi(c.NS[name].Value)
		if err != nil {
			return err
		}
		ns.NsConfs = append(ns.NsConfs, NsConf{Nsname: name, Pid: i, CloneFlag: clone})
	}
	return nil
}

func GetNamespaceValue(c *config.Config, ns string) string {
	if c.NS[ns].Value == "host" {
		return c.NS[ns].Value
	}
	if c.NS[ns].Value != "" {
		return c.NS[ns].Value
	}
	k, ok := c.NS[ns].Custom["value"]
	if ok {
		return k.(string)
	}
	return ""
}
