package config

import (
	"encoding/json"
	"log"
	"os"
)

func AllContainers() []Config {
	entries, err := os.ReadDir(Containers)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		log.Fatal(err)
	}
	var confs []Config
	for _, e := range entries {
		if e.IsDir() {
			c, err := LoadConfig(e.Name())
			if err == nil {
				confs = append(confs, c)
			}
		}
	}
	return confs
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
