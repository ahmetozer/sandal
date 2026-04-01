//go:build linux

package sandal

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/host"
	"github.com/ahmetozer/sandal/pkg/container/net"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

// RunContainer validates, sets up networking, persists, and executes a container.
// The config should have Name, ContArgs, Volumes, Lower, NS, Capabilities, etc.
// already populated (e.g. from CLI flag parsing or API input).
// networkFlags are the raw -net flag values for network interface configuration.
func RunContainer(c *config.Config, networkFlags []string) error {
	if err := config.ValidateName(c.Name); err != nil {
		return err
	}

	conts, err := controller.Containers()
	if err != nil {
		return fmt.Errorf("unable to get other container informations %s", err)
	}

	oldContStatus, err := crt.IsContainerRunning(c.Name)
	if err != nil {
		return err
	}

	if oldContStatus {
		return fmt.Errorf("container %s is already running", c.Name)
	}

	if c.Startup && !c.Background {
		return fmt.Errorf("startup only works with background mode, please enable with '-d' arg")
	}

	c.Net, err = net.ParseFlag(networkFlags, conts, c)
	if err != nil {
		return err
	}

	if err := c.NS.Defaults(); err != nil {
		return err
	}

	err = controller.SetContainer(c)
	if err != nil {
		return err
	}

	return host.Run(c)
}
