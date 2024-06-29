package cmd

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/ahmetozer/sandal/pkg/config"
	"golang.org/x/sys/unix"
)

func execOnContainer(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no container name provided")
	}
	if len(args) < 2 {
		return fmt.Errorf("no command provided")
	}
	thisFlags, childArgs := SplitFlagsArgs(args[1:])
	f := flag.NewFlagSet("exec", flag.ExitOnError)

	var (
		help bool
	)

	f.BoolVar(&help, "help", false, "show this help message")

	if err := f.Parse(thisFlags); err != nil {
		return fmt.Errorf("error parsing flags: %v", err)
	}

	if help {
		f.Usage()
	}

	conts, err := config.AllContainers()
	if err != nil {
		log.Fatalf("Failed to get containers: %v", err)
	}
	c, err := config.GetByName(&conts, args[0])
	if err != nil {
		log.Fatalf("Failed to get container %s: %v", args[0], err)
	}

	var nsFuncs []func()
	Cloneflags := unix.CLONE_NEWIPC | unix.CLONE_NEWNS | unix.CLONE_NEWCGROUP

	if c.NS["pid"].Value != "host" {
		Cloneflags |= unix.CLONE_NEWPID
		nsFuncs = append(nsFuncs, func() { setNs("pid", c.ContPid, unix.CLONE_NEWPID) })
	}
	if c.NS["net"].Value != "host" {
		Cloneflags |= unix.CLONE_NEWNET
		nsFuncs = append(nsFuncs, func() { setNs("net", c.ContPid, unix.CLONE_NEWNET) })
	}
	if c.NS["user"].Value != "host" {
		Cloneflags |= unix.CLONE_NEWUSER
		nsFuncs = append(nsFuncs, func() { setNs("user", c.ContPid, unix.CLONE_NEWUSER) })
	}
	if c.NS["uts"].Value != "host" {
		Cloneflags |= unix.CLONE_NEWUTS
		nsFuncs = append(nsFuncs, func() { setNs("uts", c.ContPid, unix.CLONE_NEWUTS) })
	}

	if err := unix.Unshare(Cloneflags); err != nil {
		log.Fatalf("Failed to unshare namespaces: %v", err)
	}
	for _, f := range nsFuncs {
		f()
	}

	setNs("pid", c.ContPid, unix.CLONE_NEWPID)
	setNs("cgroup", c.ContPid, unix.CLONE_NEWCGROUP)
	setNs("mnt", c.ContPid, unix.CLONE_NEWNS)

	// Set the hostname
	if err := unix.Sethostname([]byte(c.Name)); err != nil {
		log.Fatalf("Failed to set hostname %s: %v", c.Name, err)
	}

	var cmd *exec.Cmd
	switch len(childArgs) {
	case 0:
		return fmt.Errorf("no command provided")
	case 1:
		cmd = exec.Command(childArgs[0])
	default:
		cmd = exec.Command(childArgs[0], childArgs[1:]...)
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("unable to execute command %s rootfs %s err: %s", childArgs[0], c.RootfsDir, err.Error())
	}

	return nil
}

func setNs(nsname string, pid, nstype int) {
	path := fmt.Sprintf("/proc/%d/ns/%s", pid, nsname)
	file, err := os.Open(path)
	if err != nil {
		log.Fatalf("Failed to open namespace file %s: %v", path, err)
	}
	// defer file.Close()

	// Set the namespace
	if err := unix.Setns(int(file.Fd()), nstype); err != nil {
		log.Fatalf("Failed to set namespace %s: %v", nsname, err)
	}
}
