package cmd

import (
	"log"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/container"
)

func clear(args []string) error {
	conts, _ := config.AllContainers()
	for _, c := range conts {
		if !c.Remove {
			continue
		}
		if container.IsRunning(&c) {
			log.Printf("container %s is running, %v", c.Name, c.Remove)
			continue
		}
		deRunContainer(&c)
	}
	return nil
}
