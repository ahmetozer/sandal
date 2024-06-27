package cmd

import (
	"fmt"
	"strings"

	"github.com/ahmetozer/sandal/pkg/config"
)

func cmd(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no container name is provided")
	}
	conts, err := config.AllContainers()
	if err != nil {
		return err
	}
	for _, c := range conts {
		if c.Name == args[0] {
			fmt.Println(strings.Join(c.HostArgs, " "))
			return nil
		}
	}

	return fmt.Errorf("container '%s' is not found", args[0])
}
