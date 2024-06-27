package config

import (
	"encoding/json"
	"fmt"
	"os"
)

func AllContainers() ([]Config, error) {
	var confs []Config

	entries, err := os.ReadDir(Containers)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("there is no config exist")
		}
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			c, err := LoadConfig(e.Name())
			if err == nil {
				confs = append(confs, c)
			}
		}
	}
	return confs, nil
}

func LoadConfig(name string) (Config, error) {
	var c Config

	data, err := os.ReadFile(Containers + "/" + name + "/config.json")
	if err != nil {
		return c, err
	}

	err = json.Unmarshal(data, &c)
	if err != nil {
		return c, err
	}

	return c, nil
}
