package cmd

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ahmetozer/sandal/pkg/controller"
)

func Cmd(args []string) error {

	f := flag.NewFlagSet("", flag.ExitOnError)

	all := f.Bool("all", false, "print all")
	if len(args) < 1 && !*all {
		return fmt.Errorf("no container name is provided")
	}

	f.Parse(args)

	conts, err := controller.Containers()
	if err != nil {
		return err
	}
	for _, c := range conts {
		if c.Name == args[0] || *all {
			c.HostArgs[0] = os.Args[0] // sync with current command
			for i := range c.HostArgs {
				k := strings.Split(c.HostArgs[i], "=")
				if len(k) > 1 {
					k[1] = "\"" + k[1]
					k[len(k)-1] = k[len(k)-1] + "\""
					c.HostArgs[i] = strings.Join(k, "=")
				}
			}
			fmt.Println(strings.Join(c.HostArgs, " "))
			if !*all {
				return nil
			}

		}
	}
	if *all {
		return nil
	}

	return fmt.Errorf("container '%s' is not found", args[0])
}
