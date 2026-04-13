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

	// Resolve ContArgs from image ENTRYPOINT/CMD when not provided by CLI.
	// Docker semantics:
	//   No user command:              ENTRYPOINT + CMD
	//   User command:                 ENTRYPOINT + user_args
	//   --entrypoint X:               [X] + CMD (or user_args)
	//   --entrypoint X + user command: [X] + user_args
	entrypoint := c.ImageConfig.Entrypoint
	if c.Entrypoint != "" {
		entrypoint = []string{c.Entrypoint}
	}
	if len(c.ContArgs) == 0 {
		c.ContArgs = append(entrypoint, c.ImageConfig.Cmd...)
	} else if len(entrypoint) > 0 {
		c.ContArgs = append(entrypoint, c.ContArgs...)
	}
	if len(c.ContArgs) == 0 {
		return fmt.Errorf("no command provided and image has no ENTRYPOINT or CMD")
	}

	// Persist resolved config so guest process sees final ContArgs.
	controller.SetContainer(c)

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
