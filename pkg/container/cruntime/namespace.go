package cruntime

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

func loadNamespaceIDs(c *config.Config) {
	for _, ns := range config.Namespaces {
		if c.NS[ns].Value == "host" {
			continue
		}
		c.NS[ns].Value = readNamespace(fmt.Sprintf("/proc/%d/ns/%s", c.ContPid, ns))
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
