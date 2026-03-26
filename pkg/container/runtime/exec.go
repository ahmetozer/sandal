//go:build linux

package runtime

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	cmdline "github.com/ahmetozer/sandal/pkg/lib/cmdLine"
	"golang.org/x/sys/unix"
)

func RunCommands(c []string, chroot string, user string) {
	for _, command := range c {
		Exec(cmdline.Parse(command), chroot, user)
	}
}

// Execute under container chroot
func Exec(c []string, chroot string, user string) (exitCode int, err error) {
	var (
		cmd      *exec.Cmd
		mainRoot *os.File
	)

	switch len(c) {
	case 0:
		return 1, fmt.Errorf("empty command")
	default:
		// enter chroot
		if chroot != "" {
			mainRoot, err = os.Open("/")
			if err != nil {
				return 1, fmt.Errorf("unable to open /: %w", err)
			}
			err = unix.Chroot(chroot)
			if err != nil {
				mainRoot.Close()
				return 1, fmt.Errorf("unable to chroot %s: %w", chroot, err)
			}
		}

		execPath, err := exec.LookPath(c[0])
		if err != nil {
			return 1, fmt.Errorf("unable to find %s: %w path=%q", c[0], err, os.Getenv("PATH"))
		}

		// exit chroot
		if chroot != "" {
			err = mainRoot.Chdir()
			if err != nil {
				return 1, fmt.Errorf("unable to return to main root /: %w", err)
			}
			err = unix.Chroot(".")
			if err != nil {
				return 1, fmt.Errorf("unable to exit chroot: %w", err)
			}
		}
		slog.Debug("Exec", slog.String("exec", execPath), slog.String("args", strings.Join(c[1:], ",")))
		cmd = exec.Command(execPath, c[1:]...)
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &unix.SysProcAttr{
		// Cloneflags: unix.CLONE_NEWUTS,
	}

	u, err2 := GetUser(user)
	if err2 != nil {
		err = err2
		return
	}

	cmd.SysProcAttr.Credential = u.Credential
	if u.User != nil && u.User.HomeDir != "" {
		cmd.Dir = u.User.HomeDir
		cmd.Env = append([]string{"HOME=" + u.User.HomeDir}, cmd.Env...)
	}

	if chroot != "" {
		cmd.SysProcAttr.Chroot = chroot
	}

	if err = cmd.Start(); err != nil {
		return 1, fmt.Errorf("unable to start %s: %w", c[0], err)
	}

	err = cmd.Wait()

	if err != nil && err.Error() == "waitid: no child processes" {
		err = nil
	}

	if err != nil {
		rootfs := "container rootfs"
		if chroot != "" {
			rootfs = "main rootfs"
		}
		slog.Debug("execCommands", "command", c[0], "rootfs", rootfs, "error", err.Error())
	}

	return cmd.ProcessState.ExitCode(), err
}
