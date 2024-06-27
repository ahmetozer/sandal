package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/ahmetozer/sandal/pkg/config"
)

func inspect(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no container name is provided")
	}
	conts, _ := config.AllContainers()
	for _, c := range conts {
		if c.Name == args[0] {
			b, err := json.MarshalIndent(c, "", "\t")
			fmt.Printf("%s", b)
			return err
		}
	}
	return fmt.Errorf("container not found")
}
