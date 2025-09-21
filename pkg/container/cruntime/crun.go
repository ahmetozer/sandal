package cruntime

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/cruntime/net"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
)

type containerLog struct {
	name    string
	logType string
}

func (c containerLog) Write(p []byte) (n int, err error) {
	slog.Debug(c.name, slog.Any("msg", p))
	return len(p), nil
}

func NewLogWriter(name, t string) *containerLog {
	lw := &containerLog{}
	lw.name = name
	lw.logType = t
	return lw
}

// Container run time
func crun(c *config.Config) (int, error) {
	c.Status = ContainerStatusCreating
	var err error

	// To start proccess by daemon
	if os.Getenv("SANDAL_DAEMON_PID") == "" && c.Background && controller.GetControllerType() == controller.ControllerTypeServer {
		slog.Debug("Start", slog.Any("c.Background", c.Background), slog.Any("controller-type", controller.GetControllerType()))
		return 0, nil
	}
	cmd := exec.Command(env.BinLoc, "sandal-child", c.Name)

	if !c.Background {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		if env.Get("SANDAL_CRUN_STD", "false") == "true" {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		}
	}

	cmd.Env = childEnv(c)
	cmd.Dir = c.RootfsDir

	err = c.NS.Defaults()
	if err != nil {
		return 1, err
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: c.NS.Cloneflags(),
	}

	if c.NS.Get("user").IsUserDefined && !c.NS.Get("pid").IsHost {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Cloneflags: c.NS.Cloneflags(),
			UidMappings: []syscall.SysProcIDMap{
				{ContainerID: 0, HostID: 0, Size: procSize},
			},
			GidMappings: []syscall.SysProcIDMap{
				{ContainerID: 0, HostID: 0, Size: procSize},
			},
			Setpgid: true,
			Pgid:    os.Getppid(),
		}
	}

	// !Switching custom namespace while container create not supported.
	// ?The reason is it will impacted daemon due to proccess based impact
	// // Switch custom namespaces before starting new command
	// err = c.NS.SetNS()
	// if err != nil {
	// 	return 1, err
	// }

	go cmd.Run()

	// Process information will filled during execution
	started := time.Now()
	for cmd.Process == nil {
		time.Sleep(time.Millisecond)
		if time.Now().After(started.Add(time.Second)) {
			return 1, fmt.Errorf("unable to allocate proccess under a second")
		}
	}

	c.ContPid = cmd.Process.Pid

	// c.NS.LoadNamespaceIDs(c.ContPid)

	c.Status = ContainerStatusRunning

	if !c.NS.Get("net").IsHost {
		links, err := net.ToLinks(&c.Net)
		if err != nil {
			return 1, err
		}
		for i := range *links {
			err := (*links)[i].Create()
			if err != nil {
				return 1, err
			}
			(*links)[i].SetNsPid(c.ContPid)
		}
	}

	slog.Debug("container provisioned", "name", c.Name, "pid", c.ContPid)
	err = controller.SetContainer(c)
	if err != nil {
		return 0, err
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
				slog.Error("background container", "container", c.Name, "err", err)
				return
			}
		}()
		// err = AttachContainerToPID(c, os.Getpid())
		// if err != nil {
		// 	slog.Debug("AttachContainerToPID", slog.Any("error", err))
		// }
		return 0, nil
	}

	sig, err := cmd.Process.Wait()
	if err != nil && err.Error() == "waitid: no child processes" {
		err = nil
	}

	DeRunContainer(c)

	return sig.ExitCode(), err
}

func IsChild() bool {
	return os.Getenv("SANDAL_CHILD") != ""
}

func childEnv(c *config.Config) []string {
	if c.EnvAll {
		return appendSandalVariables(os.Environ(), c)
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

	envVars = appendSandalVariables(envVars, c)
	return envVars
}

func appendSandalVariables(s []string, c *config.Config) []string {
	s = append(s, "SANDAL_CHILD"+"="+c.Name)
	for _, r := range env.GetDefaults() {
		s = append(s, r.Name+"="+r.Cur)
	}
	return s
}
