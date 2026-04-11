//go:build linux

package forward

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/env"
	"golang.org/x/sys/unix"
)

// Environment variables used by the two-stage native helper.
const (
	// HelperEnvVar, when set, means this process was launched by the sandal
	// host to setns into a container and re-exec itself into RelayEnvVar mode.
	HelperEnvVar    = "SANDAL_FORWARD_HELPER"
	HelperPidEnvVar = "SANDAL_FORWARD_HELPER_PID"
	// RelayRunEnvVar, when set, means this process is already inside the
	// container namespaces and should run the shared RunRelay loop.
	RelayRunEnvVar = "SANDAL_FORWARD_RELAY"
)

// IsHelper returns true when this process was spawned as the native forward
// helper (outer stage — has not yet setns'd).
func IsHelper() bool { return os.Getenv(HelperEnvVar) != "" }

// IsRelayRunner returns true when this process is the inner stage running
// inside the container's namespaces.
func IsRelayRunner() bool { return os.Getenv(RelayRunEnvVar) != "" }

// HelperMain is the outer-stage entry point. It opens the container's
// ns fds, pins the thread, setns'es, then re-execs self with RelayRunEnvVar
// set so the inner stage starts clean with all goroutines in the new ns.
func HelperMain() error {
	name := os.Getenv(HelperEnvVar)
	pidStr := os.Getenv(HelperPidEnvVar)
	contPid, err := strconv.Atoi(pidStr)
	if err != nil || contPid <= 0 {
		return fmt.Errorf("invalid %s=%q", HelperPidEnvVar, pidStr)
	}

	runtime.LockOSThread()

	// Open a handle to the container's root; after setns(CLONE_NEWNS) we
	// re-anchor fs->root to it so unix-socket bind paths resolve correctly.
	rootFd, err := os.Open(fmt.Sprintf("/proc/%d/root", contPid))
	if err != nil {
		return fmt.Errorf("open container root /proc/%d/root: %w", contPid, err)
	}
	defer rootFd.Close()

	// Unshare against net+mnt so the thread has its own fs_struct (required
	// for setns(CLONE_NEWNS) which fails when fs->users > 1).
	if err := unix.Unshare(unix.CLONE_NEWNS | unix.CLONE_NEWNET); err != nil {
		return fmt.Errorf("unshare: %w", err)
	}

	for _, ns := range []struct {
		file string
		flag int
	}{
		{"net", unix.CLONE_NEWNET},
		{"mnt", unix.CLONE_NEWNS},
	} {
		f, err := os.Open(fmt.Sprintf("/proc/%d/ns/%s", contPid, ns.file))
		if err != nil {
			return fmt.Errorf("open ns %s: %w", ns.file, err)
		}
		if err := unix.Setns(int(f.Fd()), ns.flag); err != nil {
			f.Close()
			return fmt.Errorf("setns %s: %w", ns.file, err)
		}
		f.Close()
	}

	if err := unix.Fchdir(int(rootFd.Fd())); err != nil {
		return fmt.Errorf("fchdir root: %w", err)
	}
	if err := unix.Chroot("."); err != nil {
		return fmt.Errorf("chroot: %w", err)
	}
	if err := unix.Chdir("/"); err != nil {
		return fmt.Errorf("chdir /: %w", err)
	}

	// Prepare inner-stage environment and exec self. The inner process
	// runs with all goroutines freshly inside the container namespaces.
	envVars := append(os.Environ(), RelayRunEnvVar+"="+name)
	// Drop the outer-stage markers so the inner process takes the relay branch.
	filtered := envVars[:0]
	for _, kv := range envVars {
		if len(kv) >= len(HelperEnvVar)+1 && kv[:len(HelperEnvVar)+1] == HelperEnvVar+"=" {
			continue
		}
		if len(kv) >= len(HelperPidEnvVar)+1 && kv[:len(HelperPidEnvVar)+1] == HelperPidEnvVar+"=" {
			continue
		}
		filtered = append(filtered, kv)
	}
	exe := env.BinLoc
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return fmt.Errorf("executable: %w", err)
		}
	}
	slog.Debug("forward helper: re-execing into relay runner", slog.String("exe", exe))
	return syscall.Exec(exe, []string{exe, "sandal-forward-relay", name}, filtered)
}

// RelayRunnerMain is the inner-stage entry point. It loads the relay entries
// from the environment and runs the shared accept loop forever.
func RelayRunnerMain() error {
	entries, err := LoadEntriesFromEnv()
	if err != nil {
		return fmt.Errorf("decode relay entries: %w", err)
	}
	if len(entries) == 0 {
		return nil
	}
	if err := RunRelay(entries, NativeListen); err != nil {
		return err
	}
	select {} // block forever; parent will SIGTERM us on shutdown
}
