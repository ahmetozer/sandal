package cruntime

import (
	"fmt"
	"os"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/controller"
)

const (
	ContainerStatusCreating = "creating"
	ContainerStatusRunning  = "running"
	ContainerStatusStopped  = "stopped"
	ContainerStatusHang     = "hang"
)

func IsContainerRuning(name string) (bool, error) {
	oldConfig, err := controller.GetContainer(name)
	if err == nil {
		b, err := IsPidRunning(oldConfig.ContPid)
		if err != nil && oldConfig.ContPid != 0 {
			return false, fmt.Errorf("unable to check pid %d: %v", oldConfig.ContPid, err)
		}
		return b, nil

	}
	return false, nil
}

func SendSig(pid, sig int) error {
	return syscall.Kill(pid, syscall.Signal(sig))
}

var ErrPidExistenceControl = fmt.Errorf("unable to find proccess")

func IsPidRunning(pid int) (bool, error) {
	process, err := os.FindProcess(pid)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, ErrPidExistenceControl
		}
		if err == os.ErrProcessDone {
			return false, nil
		}
		return false, err
	}
	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return true, nil
	}
	if err == os.ErrProcessDone {
		return false, nil
	}
	return false, err
}
