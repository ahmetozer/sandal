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
		killErr := syscall.Kill(oldConfig.ContPid, syscall.Signal(0))
		if killErr == nil {
			return fmt.Errorf("container %s is already running on %d", oldConfig.Name, oldConfig.HostPid)
		}
	}
	return nil
}

func IsRunning(c *config.Config) bool {
	return IsPidRunning(c.ContPid)
}

func IsPidRunning(pid int) bool {
	return SendSig(pid, 0) == nil
}

func SendSig(pid, sig int) error {
	return syscall.Kill(pid, syscall.Signal(sig))
}
