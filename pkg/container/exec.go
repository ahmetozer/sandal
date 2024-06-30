package container

import (
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/net"
	"golang.org/x/sys/unix"
)

func Exec() {

	c, err := loadConfig()
	if err != nil {
		log.Fatalf("unable to load config: %s", err)
	}

	if err := unix.Sethostname([]byte(c.Name)); err != nil {
		log.Fatalf("unable to set hostname %s", err)
	}

	if c.NS["net"].Value != "host" {
		configureIfaces(&c)
	}

	childSysMounts(&c)
	childSysNodes(&c)
	execCommands(c.RunPrePivot, "/.old_root/")
	purgeOldRoot(&c)
	execCommands(c.RunPreExec, "")

	_, args := childArgs(os.Args)
	execPath, err := exec.LookPath(c.Exec)
	if err != nil {
		log.Fatalf("unable to find %s: %s", c.Exec, err)
	}
	if err := unix.Exec(execPath, append([]string{c.Exec}, args...), os.Environ()); err != nil {
		log.Fatalf("unable to exec %s: %s", c.Exec, err)
	}

}

func loadConfig() (config.Config, error) {

	config := config.NewContainer()
	confFileLoc := os.Getenv(CHILD_CONFIG_ENV_NAME)
	if confFileLoc == "" {
		return config, fmt.Errorf("config file location not present in env")
	}

	configFile, err := os.ReadFile(confFileLoc)
	if err != nil {
		return config, err
	}

	err = json.Unmarshal(configFile, &config)
	return config, err

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
			slog.Info("unable to execute", "command", command, "rootfs", rootfs, "err", err.Error())
		}
	}

}
