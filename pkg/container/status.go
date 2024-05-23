package container

import (
	"encoding/json"
	"fmt"
	"os"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/config"
)

const (
	ContainerStatusCreating = "creating"
	ContainerStatusRunning  = "running"
	ContainerStatusStopped  = "stopped"
	ContainerStatusHang     = "hang"
)

func CheckExistence(c *config.Config) error {
	configLocation := c.ConfigFileLoc()
	if _, err := os.Stat(configLocation); err == nil {
		file, err := os.ReadFile(configLocation)
		if err != nil {
			return fmt.Errorf("container config %s is exist but unable to read: %v", configLocation, err)
		}
		oldConfig := config.NewContainer()
		json.Unmarshal(file, &oldConfig)
		b, err := IsPidRunning(oldConfig.ContPid)
		if err != nil {
			return fmt.Errorf("unable to check pid %d: %v", oldConfig.ContPid, err)
		}
		if b {
			c.Status = ContainerStatusRunning
		} else {
			c.Status = ContainerStatusHang
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
