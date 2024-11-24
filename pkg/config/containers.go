package config

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
)

func Containers() ([]Config, error) {
	if isDeamon {
		return containersFromDir()
	}
	_, err := os.Stat(DaemonSocket)
	if err != nil {
		return containersFromDir()
	}
	return containersFromServer()
}

func containersFromServer() ([]Config, error) {
	var confs []Config

	httpc := http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", DaemonSocket)
			},
		},
	}

	response, err := httpc.Get("http://unix/AllContainers")
	if err != nil {
		slog.Warn("fromServer", slog.Any("err", err), slog.Any("next", "retrying from files"))
		return containersFromDir()
	}

	json.NewDecoder(response.Body).Decode(&confs)
	return confs, nil
}

func containersFromDir() ([]Config, error) {
	var confs []Config
	StateDirCreate := false
	// Create statedir for ready to allocation and query
CreateStateDir:
	entries, err := os.ReadDir(BaseStateDir)
	slog.Debug("AllContainers", slog.Any("entries", entries), slog.Any("err", err))
	if err != nil {
		if os.IsNotExist(err) {
			if StateDirCreate {
				return nil, fmt.Errorf("there is no config exist")
			} else {
				os.MkdirAll(BaseStateDir, 0o0755)
				StateDirCreate = true
				goto CreateStateDir
			}

		}
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			filepath := path.Join(BaseStateDir, e.Name())
			c, err := LoadConfig(filepath)
			slog.Debug("AllContainers", slog.Any("action", "LoadConfig"), slog.Any("filepath", filepath), slog.Any("err", err))
			if err == nil {
				confs = append(confs, c)
			}
		}
	}
	return confs, nil
}

func LoadConfig(filepath string) (Config, error) {
	var c Config

	data, err := os.ReadFile(filepath)
	if err != nil {
		return c, err
	}

	err = json.Unmarshal(data, &c)
	if err != nil {
		return c, err
	}

	return c, nil
}
