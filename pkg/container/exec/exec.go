//go:build linux

package exec

import (
	"fmt"
	"io"
	"os"
	osExec "os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/namespace"
	"golang.org/x/sys/unix"
)

// ExecInContainer enters the container's namespaces and runs a command.
//
// Uses runtime.LockOSThread() to pin the goroutine to a dedicated OS thread,
// then calls SetNS() to enter the container's namespaces. This is the same
// pattern used in kvm/vm.go:runVCPU() — other goroutines (including the
// VM init process) are completely unaffected.
//
// stdin/stdout/stderr allow the caller to provide I/O handles:
//   - CLI: os.Stdin, os.Stdout, os.Stderr
//   - Embedded controller: hijacked HTTP connection
func ExecInContainer(c *config.Config, args []string, user, dir string,
	stdin io.Reader, stdout, stderr io.Writer) error {

	if len(args) == 0 {
		return fmt.Errorf("no command specified")
	}
	if c.ContPid <= 0 {
		return fmt.Errorf("container %s has no running process", c.Name)
	}

	// Pin this goroutine to a dedicated OS thread so namespace changes
	// don't leak to other goroutines. The OS thread is released when
	// UnlockOSThread is called.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Enter the container's namespaces (PID, NET, UTS, IPC, cgroup).
	// Mount namespace is skipped because setns(CLONE_NEWNS) fails when
	// the caller is in a chroot (the VM init did chroot during VMInit).
	// Instead, we access the container's filesystem via /proc/<pid>/root.
	ns := c.NS
	if len(ns) == 0 {
		ns = namespace.DefaultsForPid(c.ContPid)
	} else {
		ns = ns.SetEmptyToPid(c.ContPid)
	}
	// Remove mount namespace — we'll use chroot on the child instead.
	// chroot() is process-wide (not per-thread), so we can't call it
	// in the controller goroutine. SysProcAttr.Chroot applies only to
	// the forked child process.
	delete(ns, "mnt")
	if err := ns.SetNS(); err != nil {
		return fmt.Errorf("setns: %w", err)
	}

	// Set hostname from container name (UTS namespace).
	unix.Sethostname([]byte(c.Name))

	// Resolve binary path inside the container's root via /proc/<pid>/root.
	// We can't use exec.LookPath because we haven't chrooted (process-wide).
	contRoot := fmt.Sprintf("/proc/%d/root", c.ContPid)
	binPath := args[0]
	if !filepath.IsAbs(binPath) {
		for _, d := range []string{"/usr/local/bin", "/usr/bin", "/bin", "/usr/local/sbin", "/usr/sbin", "/sbin"} {
			candidate := filepath.Join(contRoot, d, binPath)
			if _, err := os.Lstat(candidate); err == nil {
				binPath = filepath.Join(d, binPath)
				break
			}
		}
	}

	// Build Cmd with SysProcAttr.Chroot — applied only to the forked child.
	// Set Path directly to skip LookPath (which runs in the parent root).
	cred, homeDir := resolveUser(user)
	cmd := &osExec.Cmd{
		Path: binPath,
		Args: args,
		SysProcAttr: &syscall.SysProcAttr{
			Chroot:     contRoot,
			Credential: cred,
		},
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		Env: []string{
			"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"TERM=xterm-256color",
		},
	}
	if homeDir != "" {
		cmd.Env = append(cmd.Env, "HOME="+homeDir)
	}
	if dir != "" {
		cmd.Dir = dir
	}

	return cmd.Run()
}

// resolveUser looks up a user string (name, uid, or "user:group") and returns
// syscall credentials and home directory. Defaults to root if empty.
func resolveUser(ug string) (*syscall.Credential, string) {
	if ug == "" {
		return &syscall.Credential{Uid: 0, Gid: 0}, "/root"
	}
	u, err := user.Lookup(ug)
	if err != nil {
		// Try as numeric UID
		u, err = user.LookupId(ug)
		if err != nil {
			return &syscall.Credential{Uid: 0, Gid: 0}, "/root"
		}
	}
	uid, _ := strconv.ParseUint(u.Uid, 10, 32)
	gid, _ := strconv.ParseUint(u.Gid, 10, 32)
	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}, u.HomeDir
}
