package cruntime

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/net"
)

func Run(c *config.Config, HostIface, PodIface config.NetIface) error {
	// Starting container
	var err error
	if c.NS["net"].Value != "host" {

		if HostIface.Type == "bridge" {
			err = net.CreateIface(c, &HostIface)
			if err != nil {
				return fmt.Errorf("error creating host interface: %v", err)
			}
		}
		err = net.CreateIface(c, &PodIface)
		if err != nil {
			return fmt.Errorf("error creating pod interface: %v", err)
		}
	}

	// mount squasfs
	err = MountRootfs(c)
	if err != nil {
		return fmt.Errorf("error mount: %v", err)
	}

	// Starting proccess
	exitCode, err := Start(c, c.PodArgs)

	if !c.Remove {
		c.Status = fmt.Sprintf("exit %d", exitCode)
		if err != nil {
			c.Status = fmt.Sprintf("err %v", err)
		}
		controller.SetContainer(c)
	}
	return err
}
