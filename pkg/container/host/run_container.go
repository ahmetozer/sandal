//go:build linux

package host

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/net"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

// RunContainer is the canonical entry point shared by `sandal run` and
// `sandal build`: it validates, sets up networking, persists, and
// executes a container. The config should have Name, ContArgs, Volumes,
// Lower, NS, Capabilities, etc. already populated (e.g. from CLI flag
// parsing or API input). networkFlags are the raw -net flag values for
// network interface configuration.
//
// This used to live in pkg/sandal, but `sandal build` needs to call it
// too — and pkg/sandal imports pkg/container/build for the builder
// dispatch, so having build call pkg/sandal would create an import
// cycle. Moving it here (which pkg/sandal already imports) breaks the
// cycle without duplicating logic; pkg/sandal.RunContainer is now a
// thin wrapper around this function.
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

	return Run(c)
}
