package container

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/ahmetozer/sandal/pkg/net"
)

const (
	bits                  = 32 << (^uint(0) >> 63)
	CHILD_CONFIG_ENV_NAME = "SANDAL_CHILD"
)

var (
	procSize = 65535
)

func init() {
	// fix for cpu architecture
	if bits == 64 {
		bin := binary.BigEndian.AppendUint64([]byte{255, 255, 255, 255}, 0)
		procSize = int(binary.BigEndian.Uint32(bin))
	}

}

func Start(c *config.Config, args []string) (int, error) {
	c.Status = ContainerStatusCreating
	c.Exec, args = childArgs(args)

	cmd := exec.Command("/proc/self/exe", args...)

	if !c.Background {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	cmd.Env = childEnv(c)
	cmd.Dir = c.ContDir()

	var Cloneflags uintptr = syscall.CLONE_NEWNS | syscall.CLONE_NEWIPC | syscall.CLONE_NEWCGROUP | syscall.CLONE_NEWTIME

	if c.NS["pid"].Value != "host" {
		Cloneflags |= syscall.CLONE_NEWPID
	}
	if c.NS["net"].Value != "host" {
		Cloneflags |= syscall.CLONE_NEWNET
	}
	if c.NS["user"].Value != "host" {
		Cloneflags |= syscall.CLONE_NEWUSER
	}
	if c.NS["uts"].Value != "host" {
		Cloneflags |= syscall.CLONE_NEWUTS
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: Cloneflags,
	}

	if c.NS["user"].Value != "host" && c.NS["pid"].Value != "host" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Cloneflags: Cloneflags,
			UidMappings: []syscall.SysProcIDMap{
				{ContainerID: 0, HostID: 0, Size: procSize},
			},
			GidMappings: []syscall.SysProcIDMap{
				{ContainerID: 0, HostID: 0, Size: procSize},
			},
		}
	}

	c.SaveConftoDisk()

	err := cmd.Start()

	if err != nil {
		return 0, fmt.Errorf("starting container: %v", err)
	}
	c.ContPid = cmd.Process.Pid

	loadNamespaceIDs(c)

	c.Status = ContainerStatusRunning

	c.SaveConftoDisk()

	for _, iface := range c.Ifaces {
		if iface.ALocFor == config.ALocForPod {
			err = net.SetNs(iface, c.ContPid)
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

	if c.Background {
		go func() {
			_, err := cmd.Process.Wait()
			if err != nil {
				fmt.Printf("running container: %v", err)
				os.Exit(1)
			}
		}()
		AttachContainerToPID(c, 1)
		os.Exit(0)
	}

	sig, err := cmd.Process.Wait()
	return sig.ExitCode(), err
}

func IsChild() bool {
	return os.Getenv(CHILD_CONFIG_ENV_NAME) != ""
}

func childEnv(c *config.Config) []string {
	if c.EnvAll {
		return append(os.Environ(), CHILD_CONFIG_ENV_NAME+"="+c.ConfigFileLoc())
	}
	envVars := []string{}
	pathIsSet := false
	for _, env := range c.PassEnv {
		if env == "PATH" {
			pathIsSet = true
		}
		variable := os.Getenv(env)
		if variable == "" {
			slog.Info("enviroment variable not found", "variable", env)
		} else {
			envVars = append(envVars, fmt.Sprintf("%s=%s", env, variable))
		}
	}
	if !pathIsSet {
		envVars = append(envVars, fmt.Sprintf("PATH=%s", os.Getenv("PATH")))
	}

	return append(envVars, CHILD_CONFIG_ENV_NAME+"="+c.ConfigFileLoc())
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

func loadNamespaceIDs(c *config.Config) {
	for _, ns := range config.Namespaces {
		if c.NS[ns].Value == "host" {
			continue
		}
		c.NS[ns].Value = readNamespace(fmt.Sprintf("/proc/%d/ns/%s", c.ContPid, ns))
	}
}

func readNamespace(f string) string {
	s, err := os.Readlink(f)
	if err != nil {
		return ""
	}
	return parseNamspaceInfo(s)
}

func parseNamspaceInfo(s string) string {

	ns := strings.Split(s, "[")
	if ns == nil {
		return s
	}
	if len(ns) == 2 {
		return strings.Trim(ns[1], "]")
	}
	return s
}

func AttachContainerToPID(c *config.Config, masterPid int) error {
	if err := syscall.Setpgid(c.ContPid, masterPid); err != nil {
		return fmt.Errorf("error setting %d process group id: %s", c.ContPid, err)
	}

	if pgid, err := syscall.Getpgid(c.ContPid); err != nil || pgid != masterPid {
		return fmt.Errorf("container group pid is not verified: %s", err)
	}

	return nil
}
