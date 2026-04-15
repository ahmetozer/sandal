//go:build linux

package build

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

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
// container by delegating to the standard sandal.RunContainer() path —
// the same entry point `sandal run` uses. The container has StageRoot
// as its overlayfs lower; after it exits, the upper-dir changes are
// applied to StageRoot in-place so they are visible to subsequent
// build steps.
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
	// We want to read the upper-dir AFTER RunContainer returns. With
	// ChangeDirType=folder the change dir is a plain directory that
	// survives container teardown, so we can harvest its contents.
	// We prepare the backing ourselves (MountStageBacking) so backing
	// kind follows the same rules as stageRoot: tmpfs if -tmp is set,
	// ext4 loop image if /var/lib/sandal is on overlayfs, plain dir
	// otherwise.
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
	// Copy the host's /etc/resolv.conf and /etc/hosts into the container
	// so RUN steps can resolve hostnames (pip/apt/apk all need this).
	c.Resolv = "cp"
	c.Hosts = "cp"
	// Namespace setup: match `sandal run`'s default (user=host). sandal
	// run normally initializes c.NS via namespace.ParseFlagSet during
	// CLI parsing; build has no flag set, so we populate it manually.
	// Leaving user unset would make NS.Defaults() mark it IsHost=false,
	// which causes Cloneflags to include CLONE_NEWUSER without a
	// uid_map — the child exec then fails with EACCES. All other
	// namespaces (net, mnt, pid, ipc, uts, cgroup) default the same
	// way `sandal run` defaults them.
	hostStr := "host"
	c.NS = namespace.Namespaces{
		"user": namespace.NamespaceConf{UserValue: &hostStr},
	}

	// Forward stage ENV (from Dockerfile ENV directives and the image
	// base ENV) into the container via the officially-supported
	// -env-pass mechanism: set each KEY on the host process, list the
	// keys in c.PassEnv, and host/crun.go:childEnv will read them at
	// spawn time via os.Getenv.
	unsetEnv := applyStageEnv(c, opts.Env)
	defer unsetEnv()

	// RunContainer will:
	//  - validate c.Name
	//  - check no existing container with this name is running
	//  - parse networkFlags (nil here means "no explicit -net")
	//  - call c.NS.Defaults()
	//  - persist the container (controller.SetContainer)
	//  - invoke host.Run which mounts rootfs + spawns the process
	runErr := host.RunContainer(c, nil)

	// Reload to pick up final status/exit code persisted by host.Run.
	if reloaded, err := controller.GetContainer(name); err == nil {
		c = reloaded
	}

	// Always clean up state file and temp dirs once we've harvested
	// the upper.
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

// applyStageEnv forwards Dockerfile ENV into the container via the
// `-env-pass` path without modifying pkg/sandal, pkg/container/config,
// or pkg/container/host. It works by:
//   1. Locking the goroutine to its OS thread so the process-wide
//      env snapshot taken by host.crun.childEnv runs on this thread.
//   2. Snapshotting the previous values for each key we touch.
//   3. Setting each KEY=VALUE on the host process environment.
//   4. Populating c.PassEnv with the key list — host/crun.go:childEnv
//      reads each key via os.Getenv() at container-spawn time.
//
// The returned closure restores the previous env and unlocks the
// thread. Callers MUST invoke it via defer.
//
// Concurrency: because os.Setenv is process-global, this helper
// assumes only one RUN step at a time is in the env-mutation window.
// Build is currently serial across stages and instructions
// (builder_linux.go loops sequentially), so this holds. If parallel
// stages are ever added, gate calls to this helper behind a
// package-level sync.Mutex.
func applyStageEnv(c *config.Config, stageEnv []string) func() {
	runtime.LockOSThread()

	prev := make(map[string]*string, len(stageEnv))
	keys := make([]string, 0, len(stageEnv))
	for _, kv := range stageEnv {
		k, v, _ := strings.Cut(kv, "=")
		if k == "" {
			continue
		}
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
		runtime.UnlockOSThread()
	}
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
