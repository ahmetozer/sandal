//go:build linux

package host

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
)

func Run(c *config.Config) error {

	// When a startup container is delegated to the daemon, skip local
	// cleanup and rootfs setup — the daemon will handle the full lifecycle.
	// Only startup containers are daemon-managed; regular background (-d)
	// containers are run directly by the CLI process.
	if c.Background && c.Startup && !env.IsDaemon && controller.GetControllerType() == controller.ControllerTypeServer {
		return nil
	}

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
