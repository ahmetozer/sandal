//go:build linux

package build

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/config/wrapper"
	"github.com/ahmetozer/sandal/pkg/container/diskimage"
	"github.com/ahmetozer/sandal/pkg/container/host"
	cmount "github.com/ahmetozer/sandal/pkg/container/mount"
	"github.com/ahmetozer/sandal/pkg/container/namespace"
	"github.com/ahmetozer/sandal/pkg/container/overlayfs"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
	"golang.org/x/sys/unix"
	libbuild "github.com/ahmetozer/sandal/pkg/lib/container/build"
	"github.com/ahmetozer/sandal/pkg/lib/squashfs"
)

// StageContainer wraps the long-lived sandal container that backs one
// Dockerfile stage. RUN steps reuse the same container name, so the
// overlayfs upper accumulates changes naturally across invocations —
// exactly like `sandal run`'s controller-tracked state does. When the
// stage finishes, the merged rootfs is squashfs'd out as the stage's
// output image.
type StageContainer struct {
	Cfg      *config.Config
	buildID  string
	stageIdx int
}

// PrepareUpper materialises the change-dir backing (loop-mounted ext4
// when the host is on overlayfs, plain folder otherwise) so that COPY
// instructions can write into the upper directory before the first RUN
// triggers sandal's own mount setup. Without this, COPY writes land on
// the unmounted mount-point directory and disappear under the ext4 mount
// the first RUN performs.
//
// The mount is registered with the overlayfs package so that DeRunContainer
// (called at the start of every host.Run) cleans it up between RUN steps;
// the .img file persists, so subsequent re-mounts see the same data.
func (s *StageContainer) PrepareUpper() error {
	c := s.Cfg
	// Use sandal run's own helper so backing selection (tmpfs / image /
	// folder) and registration follow exactly the same rules. The mount
	// stays in place across all RUN steps because c.ChangeDirManaged is
	// set on the stage container.
	if _, err := overlayfs.PrepareChangeDir(c); err != nil {
		return fmt.Errorf("preparing change dir: %w", err)
	}
	return nil
}

// NewStageContainer builds the config for a stage container. Lower is
// the base image (OCI pulled .sqfs or previous-stage .sqfs). TmpSize /
// ChangeDirSize follow `sandal run` semantics — tmpfs if TmpSize > 0,
// else auto (folder when the host fs allows overlay stacking, loop
// image otherwise). The container is not started here; call ExecRun to
// execute steps against it.
func NewStageContainer(stageIdx int, buildID string, lowerSqfs string, tmpSize uint, changeDirSize string) *StageContainer {
	name := fmt.Sprintf("sandal-build-%s-stage%d", buildID, stageIdx)

	c := config.NewContainer()
	c.Name = name
	c.Lower = wrapper.StringFlags{lowerSqfs}
	c.RootfsDir = filepath.Join(env.BaseRootfsDir, name)
	c.ChangeDir = filepath.Join(env.BaseChangeDir, name)
	c.TmpSize = tmpSize
	c.ChangeDirSize = changeDirSize
	// auto picks folder when supported, image otherwise — same rule
	// `sandal run` uses. No hand-rolled MountStageBacking needed.
	c.ChangeDirType = "auto"
	// Build owns the change-dir mount across all RUN steps in a stage,
	// so host.Run won't unmount/remount it between calls — this is what
	// keeps COPY-written and earlier-RUN-written upper data persistent.
	c.ChangeDirManaged = true
	c.Background = false
	c.Remove = false
	c.TTY = false
	c.Resolv = "cp"
	c.Hosts = "cp"
	// Match `sandal run` defaults. user=host avoids a CLONE_NEWUSER
	// without a uid_map (which would fail EACCES in the child exec).
	hostStr := "host"
	c.NS = namespace.Namespaces{
		"user": namespace.NamespaceConf{UserValue: &hostStr},
	}

	return &StageContainer{Cfg: &c, buildID: buildID, stageIdx: stageIdx}
}

// ExecRun executes one RUN instruction against the stage container.
// The overlay upper persists across successive calls, so each RUN sees
// the accumulated filesystem state from prior RUN/COPY steps.
func (s *StageContainer) ExecRun(args []string, stageEnv []string, workDir, user string) error {
	if len(args) == 0 {
		return fmt.Errorf("RUN: no command")
	}

	c := s.Cfg
	// Docker RUN semantics: execute the command directly, ignoring any
	// ENTRYPOINT defined by the base image. Sandal's host.Run() only
	// skips the image-entrypoint prepend when c.Entrypoint is set, so
	// we move the first element of args into Entrypoint and pass the
	// rest as ContArgs. The final argv reconstructed by host.Run is
	// [c.Entrypoint, c.ContArgs...] — exactly the original args slice.
	c.Entrypoint = args[0]
	c.ContArgs = append([]string{}, args[1:]...)
	c.Dir = workDir
	c.User = user
	c.Status = "running"

	unset := exportStageEnv(c, stageEnv)
	defer unset()

	if err := host.RunContainer(c, nil); err != nil {
		if reloaded, rerr := controller.GetContainer(c.Name); rerr == nil {
			c.Status = reloaded.Status
		}
		return fmt.Errorf("RUN failed: %w (status: %s)", err, c.Status)
	}
	// host.Run returns nil even when the child exited non-zero — the
	// real exit code lives in c.Status as "exit N". Reload from the
	// controller and surface non-zero exits as build errors so a
	// failing RUN aborts the stage just like docker build does.
	if reloaded, err := controller.GetContainer(c.Name); err == nil {
		c.Status = reloaded.Status
	}
	if c.Status != "" && c.Status != "exit 0" {
		return fmt.Errorf("RUN exited with status: %s", c.Status)
	}
	return nil
}

// WriteUpper returns the filesystem path where COPY/ADD should write to
// land changes in the stage's overlay upper. Reuses overlayfs.GetChangeDir
// so the layout (folder vs tmpfs vs image) stays in sync with sandal run.
func (s *StageContainer) WriteUpper() string {
	return overlayfs.GetChangeDir(s.Cfg).GetUpper()
}

// Finish writes the stage's merged rootfs (Lower + accumulated Upper)
// to outPath as a squashfs.
//
// host.crun always tears down the container's mounts when the child
// exits (DeRunContainer at the end of crun), so by the time Finish
// runs the stage container's RootfsDir is gone. We re-create the
// merged view by mounting the base squashfs read-only, layering the
// stage's persisted upper directory on top via overlayfs, snapshotting
// that, then unmounting.
func (s *StageContainer) Finish(outPath string) error {
	c := s.Cfg

	cd := overlayfs.GetChangeDir(c)
	upper := cd.GetUpper()
	work := cd.GetWork()
	if err := os.MkdirAll(upper, 0755); err != nil {
		return fmt.Errorf("preparing upper for snapshot: %w", err)
	}
	if err := os.MkdirAll(work, 0755); err != nil {
		return fmt.Errorf("preparing work for snapshot: %w", err)
	}

	if len(c.Lower) == 0 {
		return fmt.Errorf("stage has no Lower base")
	}
	baseSqfs := c.Lower[0]
	img, err := diskimage.Mount(baseSqfs)
	if err != nil {
		return fmt.Errorf("mounting base squashfs %s: %w", baseSqfs, err)
	}
	defer diskimage.Umount(&img)

	mergedDir, err := os.MkdirTemp("", "sandal-build-merged-*")
	if err != nil {
		return fmt.Errorf("merged tmpdir: %w", err)
	}
	defer os.RemoveAll(mergedDir)

	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", img.MountDir, upper, work)
	if err := cmount.Mount("overlay", mergedDir, "overlay", 0, opts); err != nil {
		return fmt.Errorf("mounting merged overlay: %w", err)
	}
	defer unix.Unmount(mergedDir, 0)

	if err := writeSquashfs(mergedDir, outPath); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	return nil
}

// needsImageMount reports whether the change-dir backing requires an
// explicit ext4 loop mount. True when the parent fs is overlayfs (which
// would reject a nested overlayfs upper).
func needsImageMount(changeDir string) bool {
	parent := filepath.Dir(changeDir)
	on, _ := overlayfs.IsOverlayFS(parent)
	return on
}

// Cleanup releases all container state: unmounts the rootfs overlay
// (if still mounted), tears down the build-managed change-dir backing,
// and removes the controller record.
func (s *StageContainer) Cleanup() {
	c := s.Cfg
	// Build manages the change dir, so we must release it here. Flip
	// the flag off so DeRunContainer's normal cleanup runs.
	c.ChangeDirManaged = false
	c.Remove = true
	host.DeRunContainer(c)
	_ = controller.DeleteContainer(c.Name)
	os.RemoveAll(c.RootfsDir)
	os.RemoveAll(c.ChangeDir)
	os.RemoveAll(c.ChangeDir + ".img")
}

// exportStageEnv forwards Dockerfile ENV into the container via
// PassEnv. Sandal's host.crun.childEnv resolves PassEnv entries by
// reading os.Getenv() at spawn time, so we have to publish the values
// on the host process environment for the duration of the call. Build
// is single-threaded across stages and instructions, so no locking is
// required.
func exportStageEnv(c *config.Config, stageEnv []string) func() {
	prev := make(map[string]*string, len(stageEnv))
	keys := make([]string, 0, len(stageEnv))
	for _, kv := range stageEnv {
		eq := -1
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				eq = i
				break
			}
		}
		if eq <= 0 {
			continue
		}
		k, v := kv[:eq], kv[eq+1:]
		if old, ok := os.LookupEnv(k); ok {
			p := old
			prev[k] = &p
		} else {
			prev[k] = nil
		}
		_ = os.Setenv(k, v)
		keys = append(keys, k)
	}
	c.PassEnv = wrapper.StringFlags(keys)
	return func() {
		for _, k := range keys {
			if p := prev[k]; p != nil {
				_ = os.Setenv(k, *p)
			} else {
				_ = os.Unsetenv(k)
			}
		}
	}
}

// isOverlayMounted reports whether the given directory currently has
// an overlay mount. Used to decide whether Finish needs a warm-up run
// to mount the rootfs before snapshotting.
func isOverlayMounted(dir string) bool {
	// A mounted overlay root is a non-empty directory; if the rootfs
	// wasn't mounted, the path is either missing or an empty dir. We
	// accept false positives (empty upper + empty lower rootfs would
	// be mistaken for unmounted) — in those cases the warm-up run is
	// a harmless no-op.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	return len(entries) > 0
}

// genBuildID returns a short random hex string for naming build containers.
func genBuildID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// writeSquashfs streams srcDir into a gzip-compressed squashfs at
// outPath atomically (temp + rename).
func writeSquashfs(srcDir, outPath string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(outPath), ".build-*.sqfs.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	w, err := squashfs.NewWriter(tmp, squashfs.WithCompression(squashfs.CompGzip))
	if err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := w.CreateFromDir(srcDir); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	tmp.Close()
	return os.Rename(tmpPath, outPath)
}

// instructionArgs converts a RUN Instruction into the argv list.
//
//   - JSON exec form (Instruction.JSON == true): use Args verbatim
//   - shell form: wrap as ["/bin/sh", "-c", joined]
func instructionArgs(in libbuild.Instruction, shell []string) []string {
	if in.JSON {
		return in.Args
	}
	cmd := ""
	if len(in.Args) > 0 {
		cmd = in.Args[0]
	}
	if len(shell) == 0 {
		shell = []string{"/bin/sh", "-c"}
	}
	return append(append([]string{}, shell...), cmd)
}
