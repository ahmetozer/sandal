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
	"unsafe"

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
// When tty is true, a PTY is allocated inside the container for interactive
// shells (ash, bash, etc.) that require a terminal.
//
// stdin/stdout/stderr allow the caller to provide I/O handles:
//   - CLI: os.Stdin, os.Stdout, os.Stderr
//   - Embedded controller: hijacked HTTP connection
func ExecInContainer(c *config.Config, args []string, user, dir string, tty bool,
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
	delete(ns, "mnt")
	if err := ns.SetNS(); err != nil {
		return fmt.Errorf("setns: %w", err)
	}

	// Set hostname from container name (UTS namespace).
	unix.Sethostname([]byte(c.Name))

	// Resolve binary path inside the container's root via /proc/<pid>/root.
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

	// Build command with chroot applied only to the forked child.
	cred, homeDir := resolveUser(user)
	cmd := &osExec.Cmd{
		Path: binPath,
		Args: args,
		SysProcAttr: &syscall.SysProcAttr{
			Chroot:     contRoot,
			Credential: cred,
			Setsid:     tty, // new session for PTY
		},
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

	if tty {
		return execWithPTY(cmd, contRoot, stdin, stdout)
	}

	// Non-TTY: pipe directly
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// execWithPTY allocates a PTY inside the container's devpts, connects the
// child to the slave, and relays the master to stdin/stdout.
func execWithPTY(cmd *osExec.Cmd, contRoot string, stdin io.Reader, stdout io.Writer) error {
	// Open the PTY master from the container's /dev/ptmx via /proc/<pid>/root
	ptmxPath := filepath.Join(contRoot, "dev", "ptmx")
	master, err := os.OpenFile(ptmxPath, os.O_RDWR, 0)
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
	slavePath := filepath.Join(contRoot, fmt.Sprintf("dev/pts/%d", ptn))
	slave, err := os.OpenFile(slavePath, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open slave %s: %w", slavePath, err)
	}
	defer slave.Close()

	// Set default terminal size
	ws := unix.Winsize{Row: 24, Col: 80}
	unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &ws)

	// Connect child to slave PTY
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr.Ctty = int(slave.Fd())

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	slave.Close() // parent doesn't need the slave

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
