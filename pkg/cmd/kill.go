package cmd

import (
	"flag"
	"fmt"
	"time"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/container"
)

func kill(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no container name is provided")
	}

	thisFlags, args := SplitFlagsArgs(args)
	flags := flag.NewFlagSet("kill", flag.ExitOnError)
	var (
		help   bool
		signal int
	)
	flags.BoolVar(&help, "help", false, "show this help message")
	flags.IntVar(&signal, "signal", 9, "default kill signal")

	flags.Parse(thisFlags)

	conts, _ := config.Containers()
	for _, c := range conts {
		if c.Name == args[0] {

			ch := make(chan bool, 1)
			kill := make(chan bool)

			go func(killed chan<- bool) {
				container.SendSig(c.HostPid, 9)
				for {
					container.SendSig(c.ContPid, 9)
					b, _ := container.IsPidRunning(c.ContPid)
					if !b {
						killed <- true
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
			}(kill)

			select {
			case ret := <-kill:
				ch <- ret
			case <-time.After(5 * time.Second):
				ch <- false
			}

			stat := <-ch

			if !stat {
				return fmt.Errorf("unable to kill container pid: %d", c.ContPid)
			}

			c.Status = "killed"

			c.SaveConftoDisk()

			return nil
		}
	}
	return fmt.Errorf("container %s is not found", args[0])
}
