package container

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/net"
)

const CHILD_CONFIG_ENV_NAME = "SANDAL_CHILD"

func Start(c *config.Config, args []string) error {

	c.Exec, args = childArgs(args)

	self, err := filepath.Abs(os.Args[0])
	if err != nil {
		return fmt.Errorf("unable to get absolute path of self: %v", err)
	}
	cmd := exec.Command(self, args...)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = childEnv(c)
	cmd.Dir = c.ContDir()

	var cmdFlags uintptr = syscall.CLONE_NEWNS | syscall.CLONE_NEWIPC | syscall.CLONE_NEWCGROUP

	if c.NS.Pid != "host" {
		cmdFlags |= syscall.CLONE_NEWPID
	}
	if c.NS.Net != "host" {
		cmdFlags |= syscall.CLONE_NEWNET
	}
	if c.NS.User != "host" {
		cmdFlags |= syscall.CLONE_NEWUSER
	}
	if c.NS.Uts != "host" {
		cmdFlags |= syscall.CLONE_NEWUTS
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: cmdFlags,
	}

	if c.NS.User != "host" && c.NS.Pid != "host" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Cloneflags: cmdFlags,
			UidMappings: []syscall.SysProcIDMap{
				{ContainerID: 0, HostID: 0, Size: 4294967295},
			},
			GidMappings: []syscall.SysProcIDMap{
				{ContainerID: 0, HostID: 0, Size: 4294967295},
			},
		}
	}

	c.SaveConftoDisk()
	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("starting container: %v", err)
	}
	c.PodPid = cmd.Process.Pid

	c.SaveConftoDisk()
	for _, iface := range c.Ifaces {
		if iface.ALocFor == config.ALocForPod {
			err = net.SetNs(iface, c.PodPid)
			if err != nil {
				return fmt.Errorf("setting network namespace: %v", err)
			}
		}
	}

	cmd.Wait()
	return nil

}

func IsChild() bool {
	return os.Getenv(CHILD_CONFIG_ENV_NAME) != ""
}

func childEnv(c *config.Config) []string {
	if c.EnvAll {
		return append(os.Environ(), CHILD_CONFIG_ENV_NAME+"="+c.ConfigFileLoc())
	}
	return []string{CHILD_CONFIG_ENV_NAME + "=" + c.ConfigFileLoc()}
}

func childArgs(args []string) (string, []string) {
	if len(args) == 0 {
		return "", nil
	}
	if len(args) == 1 {
		return args[0], nil
	}
	return args[0], args[1:]
}
