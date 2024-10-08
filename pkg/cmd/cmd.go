package cmd

import (
	"fmt"
	"os"
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
			c.HostArgs[0] = os.Args[0] // sync with current command
			for i := range c.HostArgs {
				k := strings.Split(c.HostArgs[i], "=")
				if len(k) > 1 {
					k[1] = "\"" + k[1]
					k[len(k)-1] = k[len(k)-1] + "\""
					c.HostArgs[i] = strings.Join(k, "=")
				}
			}
			fmt.Println(strings.Join(c.HostArgs, " "))
			return nil
		}
	}

	return fmt.Errorf("container '%s' is not found", args[0])
}
