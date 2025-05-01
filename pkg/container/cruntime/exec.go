package cruntime

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	cmdline "github.com/ahmetozer/sandal/pkg/lib/cmdLine"
	"golang.org/x/sys/unix"
)

func runCommands(c []string, chroot string) {
	for _, command := range c {
		Exec(cmdline.Parse(command), chroot)
	}
}

// Execute under container chroot
func Exec(c []string, chroot string) (exitCode int, err error) {
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
				log.Fatalf("unable to open /: %s", err)
			}
			err = unix.Chroot(chroot)
			if err != nil {
				mainRoot.Close()
				log.Fatalf("unable to chroot %s: %s", chroot, err)
			}
		}

		execPath, err := exec.LookPath(c[0])
		if err != nil {
			log.Fatalf("unable to find %s: %s path=\"%s\"", c[0], err, os.Getenv("PATH"))
		}

		// exit chroot
		if chroot != "" {
			err = mainRoot.Chdir()
			if err != nil {
				log.Fatalf("unable return main root /: %s", err)
			}
			err = unix.Chroot(".")
			if err != nil {
				log.Fatalf("unable to exit chroot: %s", err)
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
		Cloneflags: unix.CLONE_NEWUTS,
	}

	if chroot != "" {
		cmd.SysProcAttr = &unix.SysProcAttr{
			Cloneflags: unix.CLONE_NEWUTS,
			Chroot:     chroot,
		}
	}

	err = cmd.Run()

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

	sig, _ := cmd.Process.Wait()

	return sig.ExitCode(), err
}
