package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

func SetContainer(c *config.Config) error {
CONTROLLER:
	slog.Debug("SetContainer", slog.Any("currentConrollerType", currentConrollerType))

	if err := config.ValidateName(c.Name); err != nil {
		return err
	}

	switch currentConrollerType {
	// If controller not initialized
	case 0:
		Containers()
		goto CONTROLLER
	case ControllerTypeDisk:
		return setContainerByDisk(c)
	case ControllerTypeMemory:
		return setContainerByMemory(c)
	case ControllerTypeServer:
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

	containerListMu.Lock()
	defer containerListMu.Unlock()

	// Deep-copy on the way in. The clone becomes the controller's sole
	// reference to this Config; the caller keeps their original `c`.
	// Combined with clone-on-read in containersFromMemory/GetContainer,
	// no external goroutine ever holds a pointer to the live in-memory
	// entry, so JSON encoders elsewhere can iterate map fields without
	// racing concurrent mutators.
	clone := c.Clone()
	cJSON := c.Json()

	// Only write to disk when content actually differs from what's
	// already there. This breaks the inotify feedback loop where the
	// disk-events handler reloaded the file and would re-write it
	// verbatim. A missing file (ReadFile error) reads as nil bytes and
	// reliably triggers the write.
	if diskBytes, _ := os.ReadFile(c.ConfigFileLoc()); !bytes.Equal(diskBytes, cJSON) {
		setContainerByDisk(c)
	}

	for i := range containerList {
		if containerList[i].Name == clone.Name {
			containerList[i] = clone
			return nil
		}
	}
	containerList = append(containerList, clone)
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
