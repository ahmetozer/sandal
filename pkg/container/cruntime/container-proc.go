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
		time.Sleep(8 * time.Second)
	}

	if len(c.PodArgs) < 1 {
		log.Fatalf("No executable is provided")
	}

	if err := unix.Sethostname([]byte(c.Name)); err != nil {
		log.Fatalf("unable to set hostname %s", err)
	}

	if c.NS["net"].Value != "host" {
		configureIfaces(c)
	}

	childSysMounts(c)
	childSysNodes(c)
	runCommands(c.RunPrePivot, "/.old_root/")
	purgeOldRoot(c)
	runCommands(c.RunPreExec, "")

	execPath, err := exec.LookPath(c.PodArgs[0])
	slog.Debug("Exec", slog.String("c.Exec", c.PodArgs[0]), slog.String("execPath", execPath), slog.String("args", strings.Join(c.PodArgs[1:], " ")))
	if err != nil {
		log.Fatalf("unable to find %s: %s", c.PodArgs[0], err)
	}

	if c.Dir != "/" {
		os.Chdir(c.Dir)
	}

	// Jump to real process
	if err := unix.Exec(execPath, append([]string{c.PodArgs[0]}, c.PodArgs[1:]...), os.Environ()); err != nil {
		log.Fatalf("unable to exec %s: %s", c.PodArgs[0], err)
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
