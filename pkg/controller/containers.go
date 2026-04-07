package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/env"
)

var (
	Containers    func() ([]*config.Config, error)
	containerList []*config.Config
)

func init() {
	Containers = containersInit
}

// Run at first call and attach selected method as primary method
func containersInit() ([]*config.Config, error) {
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		Containers = containersFromMemory
		currentConrollerType = ControllerTypeMemory
		// firstly load by disk
		containerList, _ = containersFromDir()
		return Containers()
	}

	if err := pingServerSocket(); err == nil {
		Containers = containersFromServer
		currentConrollerType = ControllerTypeServer
		return Containers()
	} else {
		slog.Debug("containersInit", slog.String("action", "pingServerSocket"), slog.Any("err", err))
	}

	Containers = containersFromDir
	currentConrollerType = ControllerTypeDisk
	return Containers()

}

func containersFromMemory() ([]*config.Config, error) {
	return containerList, nil
}

func containersFromServer() ([]*config.Config, error) {
	var confs []*config.Config

	httpc := http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", env.DaemonSocket)
			},
		},
	}

	response, err := httpc.Get("http://unix/containers")
	if err != nil {
		slog.Warn("fromServer", slog.Any("err", err), slog.Any("next", "retrying from files"))
		return containersFromDir()
	}

	json.NewDecoder(response.Body).Decode(&confs)
	slog.Debug("containersFromServer")
	return confs, nil
}

func containersFromDir() ([]*config.Config, error) {
	var confs []*config.Config
	StateDirCreate := false
	// Create statedir for ready to allocation and query
CreateStateDir:
	entries, err := os.ReadDir(env.BaseStateDir)
	slog.Debug("containersFromDir", slog.Any("entries", entries), slog.Any("err", err))
	if err != nil {
		if os.IsNotExist(err) {
			if StateDirCreate {
				return nil, fmt.Errorf("there is no config exist")
			} else {
				os.MkdirAll(env.BaseStateDir, 0o0755)
				StateDirCreate = true
				goto CreateStateDir
			}

		}
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			filepath := path.Join(env.BaseStateDir, e.Name())
			c, err := LoadFile(filepath)
			if err == nil {
				confs = append(confs, c)
			} else {
				slog.Warn("containersFromDir", slog.Any("action", "LoadConfig"), slog.Any("filepath", filepath), slog.Any("err", err))
			}
		}
	}
	return confs, nil
}
