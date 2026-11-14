//go:build linux

package host

import (
	"fmt"
	"slices"

	"github.com/ahmetozer/sandal/pkg/container/config"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
)

// Run sets up and starts a container. s is this run's session, nil for the
// CLI: the caller's ctx and streams, and OnPlaced, which crun invokes once
// the child's pid is published, so a caller holding the per-name lifecycle
// lock can release it as soon as placement is committed rather than across
// the foreground Wait. See crun for details.
//
// A foreground container that exits non-zero returns a *crt.ExitError with
// its code (128+signal when a signal ended it), besides the "exit N" status
// recorded for it (-1 for a signal); a background one returns once started.
func Run(c *config.Config, s *crt.RunSession) error {

	// When a startup container is delegated to the daemon, skip local
	// cleanup and rootfs setup — the daemon will handle the full lifecycle.
	// Only startup containers are daemon-managed; regular background (-d)
	// containers are run directly by the CLI process.
	if c.Background && c.Startup && !env.IsDaemon && controller.GetControllerType() == controller.ControllerTypeServer {
		return nil
	}

	DeRunContainer(c)

	// mount squasfs
	squashfsImages, err := mountRootfs(c, s)
	if err != nil {
		// mountRootfs may have already mounted some immutable lower images
		// before failing (e.g. a transient squashfs/overlay error after one
		// loop was mounted). Tear those partial mounts down so a failed
		// (re)start can't leak loop devices / immutable mounts (audit: the
		// failed-recovery storm that orphaned 16 mounts on mrs2).
		CleanupResources(c)
		return fmt.Errorf("error mount: %v", err)
	}

	// Resolve OCI image config from all pulled -lw images (last image wins).
	imgEnv, imgEntrypoint, imgCmd, imgWorkDir, imgUser := resolveImageConfig(squashfsImages)

	// Apply image defaults to fields the user did not override.
	if c.Dir == "" {
		c.Dir = imgWorkDir
	}
	if c.User == "" {
		c.User = imgUser
	}

	if err := resolveArgs(c, imgEntrypoint, imgCmd); err != nil {
		return err
	}

	// Persist resolved config so guest process sees final ContArgs.
	controller.SetContainer(c)

	// Starting proccess
	exitCode, signal, err := crun(c, imgEnv, s)

	if !c.Remove && !c.Background {
		c.Status = fmt.Sprintf("exit %d", exitCode)
		if err != nil {
			c.Status = fmt.Sprintf("err %v", err)
		}
		controller.SetContainer(c)
	}

	if err != nil {
		return err
	}
	return crt.ExitResult(exitCode, signal)
}

// resolveArgs sets c.ContArgs, the argv the container runs, from the user's
// command (c.UserArgs) and the image ENTRYPOINT/CMD. Docker semantics:
//
//	No user command:              ENTRYPOINT + CMD
//	User command:                 ENTRYPOINT + user_args
//	--entrypoint X:               [X] + CMD (or user_args)
//	--entrypoint X + user command: [X] + user_args
//
// The input is never a previous ContArgs: the daemon restarts a container
// from its persisted record, and resolving that argv again prepended the
// entrypoint once more on every recovery.
func resolveArgs(c *config.Config, imgEntrypoint, imgCmd []string) error {
	if c.UserArgs == nil {
		// A record written before UserArgs existed holds only the resolved
		// argv, so take the command from its "sandal run" command line, as
		// rerun does. A fresh config without a command has none there.
		c.UserArgs = commandFromHostArgs(c.HostArgs)
	}
	entrypoint := imgEntrypoint
	if c.Entrypoint != "" {
		entrypoint = []string{c.Entrypoint}
	}
	args := c.UserArgs
	if len(args) == 0 {
		args = imgCmd
	}
	c.ContArgs = append(slices.Clone(entrypoint), args...)
	if len(c.ContArgs) == 0 {
		return fmt.Errorf("no command provided and image has no ENTRYPOINT or CMD")
	}
	return nil
}

// commandFromHostArgs is the command after "--" in a "sandal run" command
// line ([binary, "run", flags..., "--", command...]), split as the run flag
// parser splits it; nil when there is none.
func commandFromHostArgs(hostArgs []string) []string {
	if len(hostArgs) < 2 {
		return nil
	}
	if i := slices.Index(hostArgs[2:], "--"); i >= 0 {
		return slices.Clone(hostArgs[2+i+1:])
	}
	return nil
}
