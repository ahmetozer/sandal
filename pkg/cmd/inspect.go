package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/ahmetozer/sandal/pkg/controller"
)

func Inspect(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no container name is provided")
	}

	c, err := controller.GetContainer(args[0])
	if c != nil && err == nil {
		b, _ := json.MarshalIndent(c, "", "\t")
		fmt.Printf("%s", b)
		return nil
	}

	return fmt.Errorf("container not found")
}
