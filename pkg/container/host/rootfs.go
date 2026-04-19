//go:build linux

package host

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/diskimage"
	"github.com/ahmetozer/sandal/pkg/container/overlayfs"
	cmount "github.com/ahmetozer/sandal/pkg/container/mount"
	"github.com/ahmetozer/sandal/pkg/container/snapshot"
	"github.com/ahmetozer/sandal/pkg/env"
	containerimage "github.com/ahmetozer/sandal/pkg/lib/container/image"
	"github.com/ahmetozer/sandal/pkg/lib/progress"
	"golang.org/x/sys/unix"
)

// lowerArg holds the parsed components of a -lw flag value.
type lowerArg struct {
	Source    string // host path, image ref, or disk.img:part=2
	Target    string // container mount path ("/" for root-level)
	SubMounts bool   // opt-in to sub-mount discovery via :=sub
}

// parseLowerArg parses a -lw flag value into its components.
//
// Syntax examples:
//
//	/                     -> Source="/",            Target="/",          SubMounts=false
//	/:=sub                -> Source="/",            Target="/",          SubMounts=true
//	alpine:latest         -> Source="alpine:latest",Target="/",          SubMounts=false
//	nginx:latest:/opt     -> Source="nginx:latest", Target="/opt",       SubMounts=false
//	/root:/mnt/myroot     -> Source="/root",        Target="/mnt/myroot",SubMounts=false
//	/root:/mnt/myroot:=sub-> Source="/root",        Target="/mnt/myroot",SubMounts=true
//	disk.img:part=2       -> Source="disk.img:part=2",Target="/",        SubMounts=false
func parseLowerArg(argv string) lowerArg {
	la := lowerArg{Target: "/"}

	// 1. Check for :=sub suffix.
	if strings.HasSuffix(argv, ":=sub") {
		la.SubMounts = true
		argv = strings.TrimSuffix(argv, ":=sub")
	}

	// 2. Find last occurrence of :/ to split source and target.
	if idx := strings.LastIndex(argv, ":/"); idx > 0 {
		la.Source = argv[:idx]
		la.Target = filepath.Clean(argv[idx+1:])
	} else {
		la.Source = argv
	}

	return la
}

// resolveLowerSource resolves a -lw source to a mountable directory.
// basePath is the path used for stat (disk options stripped).
// fullSource is the original source string passed to diskimage.Mount (may include :part=2).
// resolveLowerSource resolves a -lw source to a mountable directory.
// Returns (mountDir, sqfsPath, error). sqfsPath is non-empty only when
// the source was a container image reference (for image config resolution).
func resolveLowerSource(c *config.Config, basePath, fullSource string) (string, string, error) {
	fileStat, err := os.Stat(basePath)
	if err != nil {
		// Path doesn't exist on disk — check if it's a container image reference.
		if containerimage.IsImageReference(fullSource) {
			slog.Info("MountRootfs", slog.String("action", "pull-image"), slog.String("image", fullSource))

			progressCh := make(chan progress.Event, 16)
			renderDone := progress.StartRenderer(progressCh, os.Stderr)

			sqfsPath, pullErr := containerimage.Pull(context.Background(), fullSource, env.BaseImageDir, progressCh)
			close(progressCh)
			<-renderDone

			if pullErr != nil {
				return "", "", fmt.Errorf("pulling image %s: %s", fullSource, pullErr)
			}

			img, mountErr := diskimage.Mount(sqfsPath)
			if c.ImmutableImages.Contains(img) {
				c.ImmutableImages.ReplaceWith(img)
			} else {
				c.ImmutableImages = append(c.ImmutableImages, img)
			}
			if mountErr != nil {
				return "", "", fmt.Errorf("mounting pulled image: %s", mountErr)
			}
			return img.MountDir, sqfsPath, nil
		}
		return "", "", fmt.Errorf("path %s is not exist: %s", basePath, err)
	}
	if fileStat.IsDir() {
		return basePath, "", nil
	}
	// Detect file type (squashfs, ext4 image, etc.)
	img, err := diskimage.Mount(fullSource)
	if c.ImmutableImages.Contains(img) {
		c.ImmutableImages.ReplaceWith(img)
	} else {
		c.ImmutableImages = append(c.ImmutableImages, img)
	}
	if err != nil {
		slog.Debug("MountRootfs", slog.Any("img", img))
		return "", "", fmt.Errorf("mounting file: %s", err)
	}
	// If the file has a .sqfs.json sidecar, return its base path so the
	// caller can load the OCI image config. This covers two cases:
	//   1. VM mode: PullFromArgs has rewritten -lw image:tag to the cached
	//      sqfs path on the host before the VM boots; the sidecar travels
	//      with the sqfs via VirtioFS.
	//   2. User-provided -lw /path/to/my.sqfs that happens to have a sidecar.
	// LoadImageConfig returns (nil, nil) when no sidecar exists, so passing
	// the path is safe even for sqfs files without a sidecar.
	return img.MountDir, basePath, nil
}

// mountRootfs mounts the container rootfs from -lw lower sources.
// Returns the list of squashfs paths for pulled images (in -lw order),
// which the caller uses to resolve OCI image config defaults.
func mountRootfs(c *config.Config) (squashfsImages []string, err error) {
	var changeDir overlayfs.ChangesDir
	if c.ChangeDirManaged {
		// Caller (sandal build) owns the change-dir mount across runs;
		// just compute the path layout instead of cycling the backing.
		changeDir = overlayfs.GetChangeDir(c)
	} else {
		changeDir, err = overlayfs.PrepareChangeDir(c)
		if err != nil {
			return nil, fmt.Errorf("creating change directory: %s", err)
		}
	}
	slog.Debug("MountRootfs", slog.String("rootfs", c.RootfsDir), slog.String("upper", changeDir.GetUpper()), slog.String("work", changeDir.GetWork()))

	if err := os.MkdirAll(c.RootfsDir, 0755); err != nil {
		return nil, fmt.Errorf("creating workdir: %s", err)
	}

	var LowerDirs []string
	var hostDirs []string      // track directory lowerdirs for sub-mount discovery (:=sub)
	var targetedLowers []lowerArg // lowers with a custom container target path

	if len(c.Lower) == 0 {
		if len(c.Volumes) == 0 {
			return nil, fmt.Errorf("no lower dir or volume is provided")
		}
	} else {
		for _, argv := range c.Lower {
			la := parseLowerArg(argv)
			source := la.Source

			// Resolve the base path (strip disk options like :part=2) for stat/vm resolution.
			basePath := source
			if p := strings.Split(source, ":"); len(p) > 0 {
				basePath = p[0]
			}
			basePath = cmount.ResolvePath(basePath)
			slog.Debug("MountRootfs", slog.String("pathType", "lower"), slog.String("source", source), slog.String("basePath", basePath), slog.String("target", la.Target), slog.Bool("subMounts", la.SubMounts))

			// Resolve the source to a mountable directory.
			// Pass full source (with disk options) so diskimage.Mount can parse them.
			resolvedDir, sqfsPath, err := resolveLowerSource(c, basePath, source)
			if err != nil {
				return nil, err
			}
			if sqfsPath != "" {
				squashfsImages = append(squashfsImages, sqfsPath)
			}

			if la.Target == "/" {
				// Root-level: add to overlayfs lowerdir list.
				LowerDirs = append(LowerDirs, resolvedDir)
				if la.SubMounts {
					hostDirs = append(hostDirs, resolvedDir)
				}
			} else {
				// Custom target: mount as mini-overlay after main overlay.
				la.Source = resolvedDir
				targetedLowers = append(targetedLowers, la)
			}
		}
	}

	if snapshotFile := snapshot.Resolve(c); snapshotFile != "" && len(c.Lower) > 0 {
		img, err := diskimage.Mount(snapshotFile)
		if c.ImmutableImages.Contains(img) {
			c.ImmutableImages.ReplaceWith(img)
		} else {
			c.ImmutableImages = append(c.ImmutableImages, img)
		}
		if err != nil {
			return nil, fmt.Errorf("mounting snapshot: %s", err)
		}
		LowerDirs = append(LowerDirs, img.MountDir)
		slog.Debug("MountRootfs", slog.String("snapshot", snapshotFile), slog.String("mountDir", img.MountDir))
	}

	if len(LowerDirs) > 0 {
		if s, err := changeDir.IsOverlayFS(); err == nil {
			if s {
				return nil, fmt.Errorf("upper (%s) is pointed to overlayfs. Kernel does not supports creating overlayfs under overlayfs. To overcome this, you can execute your container with temporary environment '-tmp', or you can point upper directory to real disk with '-udir' flag", changeDir.GetUpper())
			}
		} else {
			return nil, fmt.Errorf("unable to check overlayfs %s", err)
		}

		options := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", strings.Join(LowerDirs, ":"), changeDir.GetUpper(), changeDir.GetWork())
		err = cmount.Mount("overlay", c.RootfsDir, "overlay", 0, options)
		slog.Debug("MountRootfs", slog.String("rootfs", c.RootfsDir), slog.Any("options", options))
		if err != nil {
			slog.Info("MountRootfs", slog.String("aciton", "mount"), slog.String("type", "overlay"), slog.String("options", options), slog.String("name", c.Name), slog.Any("error", err))
			return nil, fmt.Errorf("overlay: %s", err)
		}

		if len(hostDirs) > 0 {
			if err := mountSubMountOverlays(c, hostDirs, c.ChangeDir); err != nil {
				return nil, err
			}
		}
	}

	if len(targetedLowers) > 0 {
		if err := mountTargetedLowerOverlays(c, targetedLowers, c.ChangeDir); err != nil {
			return nil, err
		}
	}

	return squashfsImages, nil

}

// resolveImageConfig loads OCI image configs from each pulled image's
// .sqfs.json sidecar and merges them with last-image-wins semantics:
//   - entrypoint, cmd, workDir, user: each field is set to the last image
//     that provides a non-empty value
//   - env: vars are accumulated in order; on duplicate keys, the later
//     image's value overrides the earlier one
//
// squashfsImages must be in the same order as the user's -lw flags.
func resolveImageConfig(squashfsImages []string) (env, entrypoint, cmd []string, workDir, user string) {
	// Ordered key list + map for env so we preserve first-seen order
	// while letting later values override earlier ones.
	envOrder := []string{}
	envMap := map[string]string{}

	for _, p := range squashfsImages {
		cfg, err := containerimage.LoadImageConfig(p)
		if err != nil {
			slog.Warn("resolveImageConfig", slog.String("path", p), slog.Any("error", err))
			continue
		}
		if cfg == nil {
			continue
		}

		if len(cfg.Entrypoint) > 0 {
			entrypoint = cfg.Entrypoint
		}
		if len(cfg.Cmd) > 0 {
			cmd = cfg.Cmd
		}
		if cfg.WorkingDir != "" {
			workDir = cfg.WorkingDir
		}
		if cfg.User != "" {
			user = cfg.User
		}

		for _, e := range cfg.Env {
			name, val, ok := strings.Cut(e, "=")
			if !ok {
				continue
			}
			if _, exists := envMap[name]; !exists {
				envOrder = append(envOrder, name)
			}
			envMap[name] = val
		}
	}

	env = make([]string, 0, len(envOrder))
	for _, name := range envOrder {
		env = append(env, name+"="+envMap[name])
	}

	slog.Debug("resolveImageConfig",
		slog.Int("env-vars", len(env)),
		slog.Any("entrypoint", entrypoint),
		slog.Any("cmd", cmd),
		slog.String("workDir", workDir),
		slog.String("user", user))

	return env, entrypoint, cmd, workDir, user
}

func UmountRootfs(c *config.Config) []error {
	errs := []error{}
	var err error

	// Unmount sub-mount mini-overlays before the main overlay.
	if subErrs := unmountSubMountOverlays(c.Name); subErrs != nil {
		errs = append(errs, subErrs...)
	}

	err = unix.Unmount(c.RootfsDir, 0)
	if err != nil {
		if !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	err = os.Remove(c.RootfsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}

	// Image mode: unmount the ext4 loop mount and detach loop device.
	// Skipped when the caller manages the change-dir backing across
	// multiple runs (sandal build, see ChangeDirManaged).
	if !c.ChangeDirManaged {
		if mount := overlayfs.GetImageChangeMount(c.ChangeDir); mount != nil {
			if cleanupErr := mount.Cleanup(); cleanupErr != nil {
				errs = append(errs, fmt.Errorf("image change dir cleanup: %w", cleanupErr))
			}
			overlayfs.UnregisterImageChangeMount(c.ChangeDir)
		}

		if c.TmpSize != 0 {
			err = unix.Unmount(overlayfs.Tmpdir(c), 0)
			if err != nil {
				if !os.IsNotExist(err) {
					errs = append(errs, err)
				}
			}
		}
	}

	for _, sq := range c.ImmutableImages {
		err := diskimage.Umount(&sq)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return errs
}
