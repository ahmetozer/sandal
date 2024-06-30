package cmd

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/ahmetozer/sandal/pkg/config"
	"golang.org/x/sys/unix"
)

func execOnContainer(args []string) error {

	thisFlags, childArgs := SplitFlagsArgs(args)

	f := flag.NewFlagSet("exec", flag.ExitOnError)

	var (
		help    bool
		EnvAll  bool
		PassEnv config.StringFlags
		Dir     string
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

	var cmd *exec.Cmd
	switch len(childArgs) {
	case 0:
		return fmt.Errorf("no container name provided")
	case 1:
		return fmt.Errorf("no command provided")
	}

	conts, err := config.AllContainers()
	if err != nil {
		return fmt.Errorf("failed to get containers: %v", err)
	}
	c, err := config.GetByName(&conts, childArgs[0])
	if err != nil {
		return fmt.Errorf("failed to get container %s: %v", childArgs[0], err)
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

	executable, err := exec.LookPath(childArgs[1])
	if err != nil {
		return fmt.Errorf("unable to find %s: %s", childArgs[1], err)
	}
	switch len(childArgs) {
	case 2:
		cmd = exec.Command(executable)
	default:
		cmd = exec.Command(executable, childArgs[2:]...)

	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = c.Dir
	if Dir != "" {
		cmd.Dir = Dir
	}

	cmd.Env = []string{}
	if EnvAll {
		cmd.Env = os.Environ()
	} else {
		pathIsSet := false
		for _, env := range PassEnv {
			if env == "PATH" {
				pathIsSet = true
			}
			variable := os.Getenv(env)
			if variable == "" {
				slog.Info("enviroment variable not found", "variable", env)
			} else {
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", env, variable))
			}
		}
		if !pathIsSet {
			cmd.Env = append(cmd.Env, fmt.Sprintf("PATH=%s", os.Getenv("PATH")))
		}
	}

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("unable to execute command %s rootfs %s err: %s", childArgs[0], c.RootfsDir, err.Error())
	}

	return nil
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
