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

	type nsConf struct {
		nsname    string
		CloneFlag int
	}

	var nsFuncs []nsConf

	Cloneflags := unix.CLONE_NEWIPC | unix.CLONE_NEWNS | unix.CLONE_NEWCGROUP

	if c.NS["pid"].Value != "host" {
		Cloneflags |= unix.CLONE_NEWPID
		nsFuncs = append(nsFuncs, nsConf{"pid", unix.CLONE_NEWPID})
	}
	if c.NS["net"].Value != "host" {
		Cloneflags |= unix.CLONE_NEWNET
		nsFuncs = append(nsFuncs, nsConf{"net", unix.CLONE_NEWNET})
	}
	if c.NS["user"].Value != "host" {
		Cloneflags |= unix.CLONE_NEWUSER
		nsFuncs = append(nsFuncs, nsConf{"user", unix.CLONE_NEWUSER})
	}
	if c.NS["uts"].Value != "host" {
		Cloneflags |= unix.CLONE_NEWUTS
		nsFuncs = append(nsFuncs, nsConf{"uts", unix.CLONE_NEWUTS})
	}

	nsFuncs = append(nsFuncs, nsConf{"pid", unix.CLONE_NEWPID})
	nsFuncs = append(nsFuncs, nsConf{"cgroup", unix.CLONE_NEWCGROUP})
	nsFuncs = append(nsFuncs, nsConf{"mnt", unix.CLONE_NEWNS})

	// Unshare the namespaces
	if err := unix.Unshare(Cloneflags); err != nil {
		return fmt.Errorf("unshare namespaces: %v", err)
	}
	// Set the namespaces
	for _, nsConf := range nsFuncs {
		if err := setNs(nsConf.nsname, c.ContPid, nsConf.CloneFlag); err != nil {
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

	return cruntime.Exec(childArgs, "")
}

func setNs(nsname string, pid, nstype int) error {
	path := fmt.Sprintf("/proc/%d/ns/%s", pid, nsname)
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open namespace file %s: %v", path, err)
	}

	// Set the namespace
	if err := unix.Setns(int(file.Fd()), nstype); err != nil {
		return fmt.Errorf("failed to set namespace %s: %v", nsname, err)
	}
	return nil
}
