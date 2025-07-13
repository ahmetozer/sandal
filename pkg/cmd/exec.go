package cmd

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/cruntime"
	"github.com/ahmetozer/sandal/pkg/controller"
	"golang.org/x/sys/unix"
)

func ExecOnContainer(args []string) error {

	thisFlags, childArgs, splitFlagErr := SplitFlagsArgs(args)

	f := flag.NewFlagSet("exec", flag.ExitOnError)

	var (
		help     bool
		EnvAll   bool
		PassEnv  config.StringFlags
		Dir      string
		contName string
	)

	f.BoolVar(&help, "help", false, "show this help message")
	f.BoolVar(&EnvAll, "env-all", false, "send all enviroment variables to container")
	f.StringVar(&Dir, "dir", "", "working directory")
	f.Var(&PassEnv, "env-pass", "pass only requested enviroment variables to container")

	if err := f.Parse(thisFlags); err != nil {
		return fmt.Errorf("error parsing flags: %v", err)
	}

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

	var NsConfs []cruntime.NsConf

	Cloneflags := unix.CLONE_NEWIPC | unix.CLONE_NEWNS | unix.CLONE_NEWCGROUP

	if c.NS["pid"].Value != "host" {
		Cloneflags |= unix.CLONE_NEWPID
		NsConfs = append(NsConfs, cruntime.NsConf{Nsname: "pid", CloneFlag: unix.CLONE_NEWPID})
	}
	if c.NS["net"].Value != "host" {
		Cloneflags |= unix.CLONE_NEWNET
		NsConfs = append(NsConfs, cruntime.NsConf{Nsname: "net", CloneFlag: unix.CLONE_NEWNET})
	}
	if c.NS["user"].Value != "host" {
		Cloneflags |= unix.CLONE_NEWUSER
		NsConfs = append(NsConfs, cruntime.NsConf{Nsname: "user", CloneFlag: unix.CLONE_NEWUSER})
	}
	if c.NS["uts"].Value != "host" {
		Cloneflags |= unix.CLONE_NEWUTS
		NsConfs = append(NsConfs, cruntime.NsConf{Nsname: "uts", CloneFlag: unix.CLONE_NEWUTS})
	}

	NsConfs = append(NsConfs, cruntime.NsConf{Nsname: "pid", CloneFlag: unix.CLONE_NEWPID})
	NsConfs = append(NsConfs, cruntime.NsConf{Nsname: "cgroup", CloneFlag: unix.CLONE_NEWCGROUP})
	NsConfs = append(NsConfs, cruntime.NsConf{Nsname: "mnt", CloneFlag: unix.CLONE_NEWNS})

	// Unshare the namespaces
	if err := unix.Unshare(Cloneflags); err != nil {
		return fmt.Errorf("unshare namespaces: %v", err)
	}
	// Set the namespaces
	for _, nsConf := range NsConfs {
		if err := cruntime.SetNs(nsConf.Nsname, c.ContPid, int(nsConf.CloneFlag)); err != nil {
			return err
		}
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

	exitCode, err = cruntime.Exec(childArgs, "")
	if err != nil && strings.Contains(err.Error(), "exit status") {
		err = nil
	}
	return err
}
