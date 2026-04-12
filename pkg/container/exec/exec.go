//go:build linux

package exec

import (
	"fmt"
	"io"
	"os"
	osExec "os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/namespace"
	"github.com/ahmetozer/sandal/pkg/env"
	"golang.org/x/sys/unix"
)

// ExecInContainer enters the container's namespaces and runs a command.
//
// Uses runtime.LockOSThread() to pin the goroutine to a dedicated OS thread,
// then unshares and calls SetNS() to enter the container's namespaces
// (including mnt). Other goroutines are unaffected because the OS thread is
// effectively retired after namespace changes.
//
// When tty is true, a PTY is allocated from the container's devpts for
// interactive shells (ash, bash, etc.) that require a terminal.
//
// stdin/stdout/stderr allow the caller to provide I/O handles:
//   - CLI: os.Stdin, os.Stdout, os.Stderr
//   - Embedded controller: hijacked HTTP connection
func ExecInContainer(c *config.Config, args []string, userArg, dir string, tty bool,
	extraEnv []string, stdin io.Reader, stdout, stderr io.Writer) error {

	if len(args) == 0 {
		return fmt.Errorf("no command specified")
	}
	if c.ContPid <= 0 {
		return fmt.Errorf("container %s has no running process", c.Name)
	}

	// Pin this goroutine to a dedicated OS thread so namespace changes
	// don't leak to other goroutines. The OS thread is effectively retired
	// after we enter the container's namespaces (no UnlockOSThread).
	runtime.LockOSThread()

	// Build the namespace set for this container.
	ns := c.NS
	if len(ns) == 0 {
		ns = namespace.DefaultsForPid(c.ContPid)
	} else {
		ns = ns.SetEmptyToPid(c.ContPid)
	}

	// Open a dirfd to the container's root BEFORE switching namespaces.
	// The /proc/<pid>/root magic symlink resolves to the target task's
	// root dentry in its mount namespace. We need this fd because after
	// setns(CLONE_NEWNS) the kernel does NOT update our task->fs->root —
	// our thread's "/" still points at the old (VM-init) root dentry,
	// which isn't reachable in the container's mount namespace, so paths
	// like /dev/ptmx and /bin/ash fail to resolve. We fchdir to this fd
	// and chroot(".") below to re-anchor fs->root to the container's root.
	rootFd, err := os.Open(fmt.Sprintf("/proc/%d/root", c.ContPid))
	if err != nil {
		return fmt.Errorf("open container root: %w", err)
	}
	defer rootFd.Close()

	// Unshare against the same set first. This gives this thread its own
	// fs_struct and a fresh mount namespace, which is the precondition for
	// setns(CLONE_NEWNS) on the container's mnt ns (kernel requires
	// fs->users == 1).
	if err := ns.Unshare(); err != nil {
		return fmt.Errorf("unshare: %w", err)
	}

	// Enter the container's namespaces (pid, net, ipc, uts, cgroup, mnt).
	if err := ns.SetNS(); err != nil {
		return fmt.Errorf("setns: %w", err)
	}

	// Re-anchor fs->root to the container's root via the dirfd opened
	// above. Without this, the thread's "/" remains the stale VM-init
	// dentry and nothing under the container mnt ns is reachable.
	if err := unix.Fchdir(int(rootFd.Fd())); err != nil {
		return fmt.Errorf("fchdir container root: %w", err)
	}
	if err := unix.Chroot("."); err != nil {
		return fmt.Errorf("chroot container root: %w", err)
	}

	// Set hostname from container name (UTS namespace).
	unix.Sethostname([]byte(c.Name))

	if err := unix.Chdir("/"); err != nil {
		return fmt.Errorf("chdir /: %w", err)
	}

	os.Setenv("PATH", env.PATH)

	// Resolve the binary inside the container's PATH.
	execPath := args[0]
	if !strings.ContainsRune(execPath, '/') {
		p, err := osExec.LookPath(execPath)
		if err != nil {
			return fmt.Errorf("lookup %s in PATH=%q: %w", execPath, env.PATH, err)
		}
		execPath = p
	}

	cred, homeDir := resolveUser(userArg)
	cmd := &osExec.Cmd{
		Path: execPath,
		Args: args,
		SysProcAttr: &syscall.SysProcAttr{
			Credential: cred,
			Setsid:     tty, // new session for PTY
		},
		Env: os.Environ(),
	}
	// Force PATH to match what we just looked up against.
	cmd.Env = append(cmd.Env, "PATH="+env.PATH)
	if homeDir != "" {
		cmd.Env = append(cmd.Env, "HOME="+homeDir)
		cmd.Dir = homeDir
	}
	if tty {
		cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	}
	// Caller-supplied env vars (-env-pass / -env-all) take precedence.
	cmd.Env = append(cmd.Env, extraEnv...)
	if dir != "" {
		cmd.Dir = dir
	}

	if tty {
		// Detect host terminal size if stdin is a real terminal.
		var rows, cols uint16 = 24, 80
		if f, ok := stdin.(*os.File); ok {
			if ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ); err == nil && ws.Row > 0 && ws.Col > 0 {
				rows, cols = ws.Row, ws.Col
			}
		}
		return execWithPTY(cmd, stdin, stdout, rows, cols)
	}

	// Non-TTY: pipe directly
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// execWithPTY allocates a PTY from the container's devpts, connects the
// child to the slave, and relays the master to stdin/stdout.
func execWithPTY(cmd *osExec.Cmd, stdin io.Reader, stdout io.Writer, rows, cols uint16) error {
	// Open the PTY master from the container's /dev/ptmx. We are already
	// inside the container's mount namespace, so this resolves to the
	// container's devpts.
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open ptmx: %w", err)
	}
	defer master.Close()

	// Unlock the slave
	unlock := 0
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, master.Fd(), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		return fmt.Errorf("TIOCSPTLCK: %w", errno)
	}

	// Get slave PTY number
	var ptn uint32
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, master.Fd(), unix.TIOCGPTN, uintptr(unsafe.Pointer(&ptn))); errno != 0 {
		return fmt.Errorf("TIOCGPTN: %w", errno)
	}

	// Open the slave from the container's devpts
	slavePath := filepath.Join("/dev/pts", strconv.Itoa(int(ptn)))
	slave, err := os.OpenFile(slavePath, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open slave %s: %w", slavePath, err)
	}
	defer slave.Close()

	// Set terminal size from host (or defaults)
	ws := unix.Winsize{Row: rows, Col: cols}
	unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &ws)

	// Connect child to slave PTY
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	// Ctty must reference a fd valid in the child. After fork, Go's
	// os/exec dups stdin/stdout/stderr to fds 0/1/2 and closes extras.
	// Since cmd.Stdin = slave, fd 0 in the child IS the slave PTY.
	cmd.SysProcAttr.Ctty = 0
	cmd.SysProcAttr.Setctty = true

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	slave.Close() // parent doesn't need the slave

	// Forward SIGWINCH (terminal resize) to the PTY when stdin is a
	// real terminal. For VM exec over the management socket, stdin is a
	// net.Conn so this is a no-op — acceptable, the relay doesn't carry
	// resize events.
	if f, ok := stdin.(*os.File); ok {
		sigWinch := make(chan os.Signal, 1)
		signal.Notify(sigWinch, syscall.SIGWINCH)
		go func() {
			for range sigWinch {
				if ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ); err == nil {
					unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, ws)
				}
			}
		}()
		defer signal.Stop(sigWinch)
	}

	// Relay: master ↔ stdin/stdout
	// master → stdout (terminates when child exits → master read returns EIO)
	done := make(chan struct{})
	go func() {
		io.Copy(stdout, master)
		close(done)
	}()
	// stdin → master
	go io.Copy(master, stdin)

	// Wait for child and output relay
	cmd.Wait()
	<-done

	return nil
}

// resolveUser looks up a user string (name, uid, or "user:group") and returns
// syscall credentials and home directory. Defaults to root if empty.
func resolveUser(ug string) (*syscall.Credential, string) {
	if ug == "" {
		return &syscall.Credential{Uid: 0, Gid: 0}, "/root"
	}
	u, err := user.Lookup(ug)
	if err != nil {
		u, err = user.LookupId(ug)
		if err != nil {
			return &syscall.Credential{Uid: 0, Gid: 0}, "/root"
		}
	}
	uid, _ := strconv.ParseUint(u.Uid, 10, 32)
	gid, _ := strconv.ParseUint(u.Gid, 10, 32)
	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}, u.HomeDir
}
