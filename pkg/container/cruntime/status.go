package cruntime

import (
	"fmt"
	"os"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/controller"
)

const (
	ContainerStatusCreating = "creating"
	ContainerStatusRunning  = "running"
	ContainerStatusStopped  = "stopped"
	ContainerStatusHang     = "hang"
)

func CheckExistence(c *config.Config) error {
	oldConfig, err := controller.GetContainer(c.Name)
	if err == nil {
		b, err := IsPidRunning(oldConfig.ContPid)
		if err != nil && oldConfig.ContPid != 0 {
			return fmt.Errorf("unable to check pid %d: %v", oldConfig.ContPid, err)
		}
		if b {
			c.Status = ContainerStatusRunning
			controller.SetContainer(c)
		} else {
			c.Status = ContainerStatusHang
			controller.SetContainer(c)
		}
	}
	return nil
}

func IsRunning(c *config.Config) bool {
	b, _ := IsPidRunning(c.ContPid)
	return b
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
