package cmd

import (
	"flag"
	"fmt"
	"log"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/container"
)

func clear(args []string) error {

	f := flag.NewFlagSet("exec", flag.ExitOnError)

	var (
		help      bool
		deleteAll bool
	)

	f.BoolVar(&help, "help", false, "show this help message")
	f.BoolVar(&deleteAll, "all", false, "delete all containers which they are not in running state")

	if err := f.Parse(args); err != nil {
		return fmt.Errorf("error parsing flags: %v", err)
	}

	if help {
		f.Usage()
		return nil
	}

	conts, _ := config.AllContainers()
	for _, c := range conts {
		if !deleteAll {
			if !c.Remove {
				continue
			}
		}
		if container.IsRunning(&c) {
			log.Printf("container %s is running,  rm=%v", c.Name, c.Remove)
			continue
		}
		deRunContainer(&c)
	}
	return nil
}
