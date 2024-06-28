package cmd

import (
	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/container"
)

func clear(args []string) error {
	conts, _ := config.AllContainers()
	for _, c := range conts {
		if !c.Remove {
			continue
		}
		if !container.IsRunning(&c) {
			deRunContainer(&c)
		}
	}
	return nil
}
