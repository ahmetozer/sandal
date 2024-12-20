package cruntime

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/net"
	"golang.org/x/sys/unix"
)

func ContainerProc() {

	var (
		err error
		c   *config.Config
	)
	for retry := 1; retry < 10; retry++ {
		c, err = controller.GetContainer(os.Getenv("SANDAL_CHILD"))
		if err == nil {
			break
		}
		if retry > 5 {
			log.Fatalf("unable to load config: %s", err)
		}
		retry++
		time.Sleep(8 * time.Second)
	}

	if err := unix.Sethostname([]byte(c.Name)); err != nil {
		log.Fatalf("unable to set hostname %s", err)
	}

	if c.NS["net"].Value != "host" {
		configureIfaces(c)
	}

	childSysMounts(c)
	childSysNodes(c)
	execCommands(c.RunPrePivot, "/.old_root/")
	purgeOldRoot(c)
	execCommands(c.RunPreExec, "")

	_, args := childArgs(os.Args)
	execPath, err := exec.LookPath(c.Exec)
	slog.Debug("Exec", slog.String("c.Exec", c.Exec), slog.String("execPath", execPath), slog.String("args", strings.Join(args, " ")))
	if err != nil {
		log.Fatalf("unable to find %s: %s", c.Exec, err)
	}

	if c.Dir != "/" {
		os.Chdir(c.Dir)
	}

	if err := unix.Exec(execPath, append([]string{c.Exec}, args...), os.Environ()); err != nil {
		log.Fatalf("unable to exec %s: %s", c.Exec, err)
	}

}

func configureIfaces(c *config.Config) {
	var err error
	var ethNo uint8 = 0
	for i := range c.Ifaces {
		if c.Ifaces[i].ALocFor == config.ALocForPod {

			err = net.WaitInterface(c.Ifaces[i].Name)
			if err != nil {
				log.Fatalf("%s", err)
			}

			err = net.SetName(c, c.Ifaces[i].Name, fmt.Sprintf("eth%d", ethNo))
			if err != nil {
				log.Fatalf("unable to set name %s", err)
			}

			err = net.AddAddress(c.Ifaces[i].Name, c.Ifaces[i].IP)
			if err != nil {
				log.Fatalf("unable to add address %s", err)
			}

			err = net.SetInterfaceUp(fmt.Sprintf("eth%d", ethNo))
			if err != nil {
				log.Fatalf("unable to set eth%d up %s", ethNo, err)
			}

			if ethNo == 0 {
				net.AddDefaultRoutes(c.Ifaces[i])
			}

			ethNo++
		}
	}

	if err := net.SetInterfaceUp("lo"); err != nil {
		log.Fatalf("unable to set lo up %s", err)
	}
}

func execCommands(c []string, chroot string) {
	for _, command := range c {

		var (
			cmd      *exec.Cmd
			mainRoot *os.File
			err      error
		)
		args := strings.Split(command, " ")

		switch len(args) {
		case 0:
			return
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

			execPath, err := exec.LookPath(args[0])
			if err != nil {
				log.Fatalf("unable to find %s: %s path=\"%s\"", args[0], err, os.Getenv("PATH"))
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
			cmd = exec.Command(execPath, args[1:]...)
		}

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
		if err != nil {
			rootfs := "container rootfs"
			if chroot != "" {
				rootfs = "main rootfs"
			}
			slog.Info("execCommands", "unable to execute", "command", command, "rootfs", rootfs, slog.Any("error", err))
		}
	}

}
