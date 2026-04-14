//go:build linux

package build

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/config/wrapper"
	"github.com/ahmetozer/sandal/pkg/container/host"
	"github.com/ahmetozer/sandal/pkg/container/namespace"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
	libbuild "github.com/ahmetozer/sandal/pkg/lib/container/build"
)

// RunOpts configures one RUN instruction execution.
type RunOpts struct {
	StageRoot     string   // on-disk stage rootfs (read as overlay lower)
	BuildID       string   // for unique container name
	StepIdx       int      // for unique container name
	Args          []string // command + args (exec form) OR ["sh","-c",cmd] (shell form)
	WorkDir       string   // CWD inside the container
	User          string   // user spec (e.g. "root" or "1000:1000")
	Env           []string // KEY=VALUE pairs to inject
	TmpSize       uint     // see BuildRequest / BuildOpts
	ChangeDirSize string   // see BuildRequest / BuildOpts
}

// ExecRun runs a single RUN instruction inside an ephemeral sandal
// container that has StageRoot as its overlayfs lower. After the command
// exits, the upper-dir changes are applied to StageRoot in-place so they
// are visible to subsequent build steps.
//
// Failure of the command (non-zero exit) is propagated as an error.
func ExecRun(opts RunOpts) error {
	if len(opts.Args) == 0 {
		return fmt.Errorf("RUN: no command")
	}

	name := fmt.Sprintf("sandal-build-%s-step%d", opts.BuildID, opts.StepIdx)

	cVal := config.NewContainer()
	c := &cVal
	c.Name = name
	c.Lower = wrapper.StringFlags{opts.StageRoot}
	c.RootfsDir = filepath.Join(env.BaseRootfsDir, name)
	c.ChangeDir = filepath.Join(env.BaseChangeDir, name)
	// We want to read the upper-dir AFTER host.Run returns. UmountRootfs
	// only touches the change dir for "image" mode (loop unmount + delete)
	// or when TmpSize>0 (unmounts tmpfs). With "folder" it leaves the
	// directory alone, so we can harvest its contents. We prepare the
	// backing ourselves (MountStageBacking) so the upper dir survives
	// derun cleanup. Backing kind follows the same rules as stageRoot:
	// tmpfs if -tmp is set, ext4 loop image if /var/lib/sandal is on
	// overlayfs, plain dir otherwise.
	c.ChangeDirType = "folder"
	cleanupChange, err := MountStageBacking(c.ChangeDir, opts.TmpSize, opts.ChangeDirSize)
	if err != nil {
		return fmt.Errorf("change dir backing: %w", err)
	}
	defer func() {
		_ = cleanupChange()
		_ = os.Remove(c.ChangeDir)
	}()
	c.ContArgs = opts.Args
	c.Background = false
	c.Remove = false // we read upper-dir AFTER run, then clean up
	c.TTY = false
	c.Dir = opts.WorkDir
	c.User = opts.User
	c.Status = "running"
	// Copy the host's /etc/resolv.conf into the container so RUN steps
	// can resolve hostnames (pip/apt/apk all need this).
	c.Resolv = "cp"
	c.Hosts = "cp"
	// Build container: isolate mount/pid/uts/ipc/cgroup, but use host
	// network (so RUN can fetch packages) and host user (creating a new
	// user namespace requires uid_map configuration we don't do here).
	hostStr := "host"
	c.NS = namespace.Namespaces{
		"net":  namespace.NamespaceConf{UserValue: &hostStr, IsHost: true, IsUserDefined: true},
		"user": namespace.NamespaceConf{UserValue: &hostStr, IsHost: true, IsUserDefined: true},
	}
	if err := c.NS.Defaults(); err != nil {
		return fmt.Errorf("namespace defaults: %w", err)
	}
	c.HostArgs = []string{env.BinLoc, "run", "-name", name, "-lw", opts.StageRoot, "--"}
	c.HostArgs = append(c.HostArgs, opts.Args...)

	// host.RunWithExtraEnv mounts overlay, runs, unmounts; returns when
	// the command exits.
	runErr := host.RunWithExtraEnv(c, opts.Env)

	// Reload to pick up final status/exit code persisted by host.Run.
	if reloaded, err := controller.GetContainer(name); err == nil {
		c = reloaded
	}

	// Always clean up state file and temp dirs once we've harvested the upper.
	defer func() {
		os.RemoveAll(c.RootfsDir)
		os.RemoveAll(c.ChangeDir + ".img")
		_ = controller.DeleteContainer(name)
	}()

	if runErr != nil {
		os.RemoveAll(c.ChangeDir)
		return fmt.Errorf("RUN failed: %w (status: %s)", runErr, c.Status)
	}

	// Apply upper-dir changes to StageRoot.
	upper := filepath.Join(c.ChangeDir, "upper")
	if _, err := os.Stat(upper); err == nil {
		if err := applyOverlayUpper(upper, opts.StageRoot); err != nil {
			os.RemoveAll(c.ChangeDir)
			return fmt.Errorf("merging RUN changes: %w", err)
		}
	} else {
		slog.Debug("ExecRun: no upper dir to apply", "path", upper)
	}
	os.RemoveAll(c.ChangeDir)
	return nil
}

// genBuildID returns a short random hex string for naming build containers.
func genBuildID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// instructionArgs converts a RUN/CMD/ENTRYPOINT-style Instruction into the
// argv list for execution.
//
//   - JSON exec form (Instruction.JSON == true): use Args verbatim
//   - shell form: wrap as ["/bin/sh", "-c", joined]
//
// shell is the SHELL-instruction override (or nil for default ["/bin/sh","-c"]).
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
