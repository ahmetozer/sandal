package cmd

import (
	"fmt"
	"os"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/container"
	"golang.org/x/sys/unix"
)

func rerun(args []string) error {
	if len(args) == 0 || args[0] == "" {
		return fmt.Errorf("no container name provided")
	} else if args[0] == "help" {
		fmt.Printf("%s restart ${container id}\n", os.Args[0])
		exitCode = 0
		return nil
	}

	for _, c := range config.AllContainers() {
		if c.Name == args[0] {

			if container.IsRunning(&c) {
				if container.IsPidRunning(c.HostPid) {

					container.SendSig(c.HostPid, 15)
					if container.IsPidRunning(c.HostPid) {
						container.SendSig(c.HostPid, 9)
					}
					if container.IsRunning(&c) {
						container.SendSig(c.ContPid, 9)
					}

					deRunContainer(&c)
					if err := unix.Exec("/proc/self/exe", c.HostArgs, os.Environ()); err != nil {
						return fmt.Errorf("unable to restart %s", err)
					}
				}
			}

		}
	}

	return fmt.Errorf("container %s not found", args[0])
}
