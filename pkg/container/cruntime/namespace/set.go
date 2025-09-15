package namespace

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func (NS Namespaces) SetNS() error {
	for nsName, nsConf := range NS {
		if nsConf.Pid == 0 {
			continue
		}
		if nsConf.host {
			continue
		}
		if err := setNs(nsName, nsConf.Pid, int(namespaceList[nsName])); err != nil {
			return fmt.Errorf("namespaces set ns: %s", err)
		}
	}
	return nil
}

func setNs(nsname string, pid, nstype int) error {
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
