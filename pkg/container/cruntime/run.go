package cruntime

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/controller"
)

func Run(c *config.Config) error {

	DeRunContainer(c)

	// mount squasfs
	err := mountRootfs(c)
	if err != nil {
		return fmt.Errorf("error mount: %v", err)
	}

	// Starting proccess
	exitCode, err := crun(c)

	if !c.Remove && !c.Background {
		c.Status = fmt.Sprintf("exit %d", exitCode)
		if err != nil {
			c.Status = fmt.Sprintf("err %v", err)
		}
		controller.SetContainer(c)
	}

	return err
}
