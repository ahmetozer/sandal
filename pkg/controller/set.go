package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

func SetContainer(c *config.Config) error {
CONTROLLER:
	slog.Debug("SetContainer", slog.Any("currentConrollerType", currentConrollerType))

	if c.Name == "" {
		return fmt.Errorf("no name set for request")
	}

	switch currentConrollerType {
	// If controller not initialized
	case 0:
		Containers()
		goto CONTROLLER
	case controllerTypeDisk:
		return setContainerByDisk(c)
	case controllerTypeMemory:
		return setContainerByMemory(c)
	case controllerTypeServer:
		return setContainerByServer(c)
	default:
		return fmt.Errorf("unknown controller type")
	}
}

func setContainerByServer(c *config.Config) error {
	slog.Debug("setContainerByServer", slog.Any("container", c.Name))

	jsonValue, _ := json.Marshal(c)
	_, err := httpc.Post("http://unix/containers/"+c.Name, "text/json", bytes.NewBuffer(jsonValue))
	return err
}

func setContainerByMemory(c *config.Config) error {
	slog.Debug("setContainerByMemory", slog.Any("container", c.Name))

	for i := range containerList {
		if containerList[i].Name == c.Name {
			if strings.Join(containerList[i].HostArgs, " ") != strings.Join(c.HostArgs, " ") {
				setContainerByDisk(c)
			}
			containerList[i] = c
			return nil
		}
	}
	containerList = append(containerList, c)
	setContainerByDisk(c)

	return nil
}

func setContainerByDisk(c *config.Config) error {
	slog.Debug("setContainerByDisk", slog.Any("container", c.Name))

	if c.Name == "" {
		return fmt.Errorf("no name set for request")
	}

	savePath := filepath.Dir(c.ConfigFileLoc())
	slog.Debug("ConfigFileLoc", slog.String("action", "saving config file"), slog.String("file", c.ConfigFileLoc()))
	retry := false
WriteFile:
	err := os.WriteFile(c.ConfigFileLoc(), c.Json(), 0o0644)
	if err != nil {
		if os.IsNotExist(err) && !retry {
			err := os.MkdirAll(savePath, 0o0755)
			slog.Debug("ConfigFileLoc", slog.String("action", "mkdir conf path"), slog.String("path", savePath), slog.Any("error", err))
			retry = true
			goto WriteFile
		}
		return fmt.Errorf("writing config file: %v", err)
	}
	return nil
}
