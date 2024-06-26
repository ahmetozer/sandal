package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/container"
	"golang.org/x/sys/unix"
)

func rerun(args []string) error {
	if len(args) == 0 || args[0] == "" {
		return fmt.Errorf("no container name provided")
	} else if args[0] == "help" {
		fmt.Printf("%s rerun ${container id}\n", os.Args[0])
		exitCode = 0
		return nil
	}
	conts, _ := config.AllContainers()

	for _, c := range conts {
		if c.Name == args[0] {

			err := fmt.Errorf("unable to stop container %s", c.Name)

			go func() {
				for {
					container.SendSig(c.ContPid, 9)
					container.SendSig(c.HostPid, 9)
					b, _ := container.IsPidRunning(c.ContPid)
					if !b {
						break
					}

					time.Sleep(100 * time.Millisecond)
				}

				if err2 := unix.Exec("/usr/bin/sandal", c.HostArgs, os.Environ()); err2 != nil {
					err = fmt.Errorf("unable to rerun %s", err2)
				}
			}()

			time.Sleep(5 * time.Second)
			return err
		}
	}

	return fmt.Errorf("container %s not found", args[0])
}
