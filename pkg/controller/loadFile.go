package controller

import (
	"encoding/json"
	"os"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

func LoadFile(filepath string) (*config.Config, error) {
	var c config.Config

	data, err := os.ReadFile(filepath)
	if err != nil {
		return &c, err
	}

	err = json.Unmarshal(data, &c)
	if err != nil {
		return &c, err
	}

	return &c, nil
}
