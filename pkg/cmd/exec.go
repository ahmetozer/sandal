package cmd

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ahmetozer/sandal/pkg/container/config/wrapper"
	"github.com/ahmetozer/sandal/pkg/container/cruntime"
	"github.com/ahmetozer/sandal/pkg/container/cruntime/namespace"
	"github.com/ahmetozer/sandal/pkg/controller"
	"golang.org/x/sys/unix"
)

func ExecOnContainer(args []string) error {
	thisFlags, childArgs, splitFlagErr := SplitFlagsArgs(args)

	f := flag.NewFlagSet("exec", flag.ExitOnError)

	var (
		help     bool
		EnvAll   bool
		PassEnv  wrapper.StringFlags
		Dir      string
		User     string
		contName string
	)

	f.BoolVar(&help, "help", false, "show this help message")
	f.BoolVar(&EnvAll, "env-all", false, "send all enviroment variables to container")
	f.StringVar(&Dir, "dir", "", "working directory")
	f.StringVar(&User, "user", "", "work user")

	f.Var(&PassEnv, "env-pass", "pass only requested enviroment variables to container")

	// Allocate variable locations
	NS := namespace.ParseFlagSet(f)

	if err := f.Parse(thisFlags); err != nil {
		return fmt.Errorf("error parsing flags: %v", err)
	}
	// Execute after parsing flag to prevent nil pointer issues or empty variable
	NS.Defaults()

	if help {
		f.Usage()
		return nil
	}

	if splitFlagErr != nil {
		return splitFlagErr
	}

	switch len(f.Args()) {
	case 0:
		return fmt.Errorf("please provide name or provide name after arguments")
	case 1:
	default:
		return fmt.Errorf("multiple unrecognized name provided, please provide only one %v", f.Args())
	}

	contName = f.Args()[0]

	c, err := controller.GetContainer(contName)
	if err != nil {
		return fmt.Errorf("failed to get container %s: %v", contName, err)
	}

	merge := c.NS.SetEmptyToPid(c.ContPid).Merge(NS)

	if err := merge.Unshare(); err != nil {
		return err
	}

	err = merge.SetNS()
	if err != nil {
		return err
	}

	// Set the hostname
	if err := unix.Sethostname([]byte(c.Name)); err != nil {
		return fmt.Errorf("set hostname %s: %v", c.Name, err)
	}

	if !EnvAll {
		PassEnv = append(PassEnv, "PATH")
		for _, e := range os.Environ() {
			key := strings.Split(e, "=")[0]
			if !isIn((*[]string)(&PassEnv), key) {
				os.Unsetenv(key)
			}
		}
	}

	if User == "" {
		User = c.User
	}
	exitCode, err = cruntime.Exec(childArgs, "", User)
	if err != nil && strings.Contains(err.Error(), "exit status") {
		err = nil
	}
	return err
}
