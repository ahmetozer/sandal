package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
)

func AllContainers() ([]Config, error) {
	var confs []Config
	StateDirCreate := false
CreateStateDir:
	entries, err := os.ReadDir(BaseStateDir)
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
		if e.IsDir() {
			c, err := LoadConfig(path.Join(BaseStateDir, e.Name()))
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
