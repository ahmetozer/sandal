package container

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/net"
)

const CHILD_CONFIG_ENV_NAME = "SANDAL_CHILD"

func Start(c *config.Config, args []string) (int, error) {

	c.Exec, args = childArgs(args)

	cmd := exec.Command("/proc/self/exe", args...)

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
	err := cmd.Start()
	if err != nil {
		return 0, fmt.Errorf("starting container: %v", err)
	}
	c.PodPid = cmd.Process.Pid

	c.SaveConftoDisk()
	for _, iface := range c.Ifaces {
		if iface.ALocFor == config.ALocForPod {
			err = net.SetNs(iface, c.PodPid)
			if err != nil {
				return 0, fmt.Errorf("setting network namespace: %v", err)
			}
		}
	}

	go func() {
		done := make(chan os.Signal, 1)
		for {
			signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGTSTP, syscall.SIGCONT, syscall.SIGCHLD, syscall.SIGABRT, syscall.SIGUSR1, syscall.SIGUSR2, syscall.SIGWINCH, syscall.SIGIO, syscall.SIGURG, syscall.SIGPIPE, syscall.SIGALRM, syscall.SIGVTALRM, syscall.SIGPROF, syscall.SIGSYS, syscall.SIGTRAP, syscall.SIGBUS, syscall.SIGSEGV, syscall.SIGILL, syscall.SIGFPE, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
			cmd.Process.Signal(<-done)
		}
	}()

	cmd.Wait()
	sig, _ := cmd.Process.Wait()

	return sig.ExitCode(), nil
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
