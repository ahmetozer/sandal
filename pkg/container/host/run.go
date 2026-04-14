//go:build linux

package host

import (
	"fmt"
	"strings"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
)

// mergeEnv combines two KEY=VALUE env lists. On duplicate keys, the later
// list (extra) wins. Order: base entries first, then extra entries that
// don't override a base key.
func mergeEnv(base, extra []string) []string {
	idx := map[string]int{}
	out := make([]string, len(base))
	copy(out, base)
	for i, e := range out {
		k, _, _ := strings.Cut(e, "=")
		idx[k] = i
	}
	for _, e := range extra {
		k, _, _ := strings.Cut(e, "=")
		if i, ok := idx[k]; ok {
			out[i] = e
		} else {
			idx[k] = len(out)
			out = append(out, e)
		}
	}
	return out
}

func Run(c *config.Config) error {
	return RunWithExtraEnv(c, nil)
}

// RunWithExtraEnv is identical to Run but also injects extraEnv into the
// container's environment. Used by `sandal build` to pass per-stage ENV
// (which isn't backed by a .sqfs.json sidecar) into RUN steps.
//
// extraEnv entries are appended after the image's ENV; on duplicate keys
// extraEnv wins (it's the "more specific" source).
func RunWithExtraEnv(c *config.Config, extraEnv []string) error {

	// When a startup container is delegated to the daemon, skip local
	// cleanup and rootfs setup — the daemon will handle the full lifecycle.
	// Only startup containers are daemon-managed; regular background (-d)
	// containers are run directly by the CLI process.
	if c.Background && c.Startup && !env.IsDaemon && controller.GetControllerType() == controller.ControllerTypeServer {
		return nil
	}

	DeRunContainer(c)

	// mount squasfs
	squashfsImages, err := mountRootfs(c)
	if err != nil {
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

	// Resolve ContArgs from image ENTRYPOINT/CMD when not provided by CLI.
	// Docker semantics:
	//   No user command:              ENTRYPOINT + CMD
	//   User command:                 ENTRYPOINT + user_args
	//   --entrypoint X:               [X] + CMD (or user_args)
	//   --entrypoint X + user command: [X] + user_args
	entrypoint := imgEntrypoint
	if c.Entrypoint != "" {
		entrypoint = []string{c.Entrypoint}
	}
	if len(c.ContArgs) == 0 {
		c.ContArgs = append(entrypoint, imgCmd...)
	} else if len(entrypoint) > 0 {
		c.ContArgs = append(entrypoint, c.ContArgs...)
	}
	if len(c.ContArgs) == 0 {
		return fmt.Errorf("no command provided and image has no ENTRYPOINT or CMD")
	}

	// Persist resolved config so guest process sees final ContArgs.
	controller.SetContainer(c)

	// Merge extraEnv into imgEnv (extraEnv wins on duplicate keys).
	if len(extraEnv) > 0 {
		imgEnv = mergeEnv(imgEnv, extraEnv)
	}

	// Starting proccess
	exitCode, err := crun(c, imgEnv)

	if !c.Remove && !c.Background {
		c.Status = fmt.Sprintf("exit %d", exitCode)
		if err != nil {
			c.Status = fmt.Sprintf("err %v", err)
		}
		controller.SetContainer(c)
	}

	return err
}
