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
	"github.com/ahmetozer/sandal/pkg/container/cruntime/resources"
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

	// Setup resource limits via cgroups
	var cgroupPath string
	if c.MemoryLimit != "" || c.CPULimit != "" {
		cgroupPath, err = resources.SetupCgroup(c.Name, c.MemoryLimit, c.CPULimit)
		if err != nil {
			return 1, fmt.Errorf("cgroup setup failed: %w", err)
		}
		slog.Debug("cgroup created", "path", cgroupPath)

		// Generate custom proc files for resource visibility
		err = resources.GenerateProcFiles(c.RootfsDir, c.MemoryLimit, c.CPULimit)
		if err != nil {
			return 1, fmt.Errorf("proc file generation failed: %w", err)
		}
	}

	// Get clone flags for namespaces
	cloneFlags := c.NS.Cloneflags()

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: cloneFlags,
	}

	if c.NS.Get("user").IsUserDefined && !c.NS.Get("pid").IsHost {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Cloneflags: cloneFlags,
			UidMappings: []syscall.SysProcIDMap{
				{ContainerID: 0, HostID: 0, Size: procSize},
			},
			GidMappings: []syscall.SysProcIDMap{
				{ContainerID: 0, HostID: 0, Size: procSize},
			},
			// Only set process group for background containers
			// Interactive containers need to stay in the same process group to receive terminal signals
			Setpgid: c.Background,
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

	// Move container process into cgroup
	if cgroupPath != "" {
		err = resources.AddProcess(cgroupPath, c.ContPid)
		if err != nil {
			slog.Warn("failed to add process to cgroup", "error", err)
			// Don't fail container, limits may not be critical
		} else {
			slog.Debug("process added to cgroup", "path", cgroupPath, "pid", c.ContPid)
		}
	}

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

	// Forward signals to the container process
	// For termination signals (SIGINT, SIGTERM, SIGQUIT), use SIGKILL since
	// PID 1 in a namespace ignores signals unless it has explicit handlers.
	// SIGKILL cannot be ignored and ensures proper container termination.
	if !c.Background {
		go func() {
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan,
				syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT,
				syscall.SIGTSTP, syscall.SIGCONT, syscall.SIGWINCH,
				syscall.SIGUSR1, syscall.SIGUSR2,
			)
			defer signal.Stop(sigChan)

			for sig := range sigChan {
				if cmd.Process != nil {
					// Convert termination signals to SIGKILL for PID 1 in namespace
					signalToSend := sig
					if sig == syscall.SIGINT || sig == syscall.SIGTERM || sig == syscall.SIGQUIT {
						signalToSend = syscall.SIGKILL
					}

					if err := cmd.Process.Signal(signalToSend); err != nil {
						slog.Debug("failed to forward signal", "signal", sig, "error", err)
						return
					}
					slog.Debug("forwarded signal to container", "original", sig, "sent", signalToSend, "pid", cmd.Process.Pid)
				}
			}
		}()
	}

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
