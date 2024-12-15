package controller

import (
	"fmt"
	"log/slog"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

func GetContainer(Name string) (*config.Config, error) {
	conts, err := Containers()
	if err != nil {
		slog.Debug("GetContainer", slog.String("name", Name), slog.Any("error", err))
		return &config.Config{}, err
	}
	for i := range conts {
		if conts[i].Name == Name {
			return conts[i], nil
		}
	}
	slog.Debug("GetContainer", slog.String("name", Name), slog.String("msg", "container not found"))
	return nil, fmt.Errorf("container, not found")

}
