package cruntime

import (
	"fmt"
	"syscall"
)

func AttachContainerToPID(containerPid, masterPid int) error {
	if err := syscall.Setpgid(containerPid, masterPid); err != nil {
		return fmt.Errorf("error setting %d process group id: %s", containerPid, err)
	}
	if pgid, err := syscall.Getpgid(containerPid); err != nil || pgid != masterPid {
		return fmt.Errorf("container group pid is not verified: %s", err)
	}
	return nil
}
