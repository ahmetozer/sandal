//go:build linux

package host

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/console"
	"github.com/ahmetozer/sandal/pkg/container/net"
	"github.com/ahmetozer/sandal/pkg/container/resources"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
	"golang.org/x/sys/unix"
)

// Container run time.
//
// onPlaced, if non-nil, is invoked exactly once right after the child's pid+
// identity have been published — i.e. once placement is committed and a
// concurrent IsContainerRunning would observe this container. Callers pass the
// lifecycle-lock release here so the lock is dropped as soon as placement is
// done, rather than being held across the (possibly long-lived) foreground
// Wait. It is NOT called on the daemon-delegation early return, where no local
// placement happens.
func crun(c *config.Config, imageEnv []string, onPlaced func()) (exitCode int, retErr error) {
	c.Status = crt.ContainerStatusCreating
	var err error

	// Only delegate to daemon when the container has Startup flag set.
	// Regular background (-d) containers are started directly by the CLI.
	if !env.IsDaemon && c.Background && c.Startup && controller.GetControllerType() == controller.ControllerTypeServer {
		slog.Debug("Start", slog.Any("c.Background", c.Background), slog.Any("controller-type", controller.GetControllerType()))
		return 0, nil
	}
	cmd := exec.Command(env.BinLoc, "sandal-child", c.Name)

	// PTY master/slave for interactive VM containers; nil otherwise
	var ptmx, ptySlave *os.File
	var consoleCleanup func()

	if !c.Background {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else if c.TTY && env.IsDaemon {
		// Background + daemon + TTY: PTY-based console served over Unix socket.
		// Requires daemon because the socket listener and PTY master must outlive the CLI.
		console.SetupSocketConsole(c.Name, cmd, &ptmx, &ptySlave, &consoleCleanup, allocPTY, setPTYSize)
	} else {
		// Daemonless background or no TTY: FIFO/file-based console
		if err := console.SetupFIFOConsole(c.Name, cmd, &consoleCleanup); err != nil {
			return 1, fmt.Errorf("console setup: %w", err)
		}
	}

	cmd.Env = childEnv(c, imageEnv)
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

	// Preserve Setsid/Setctty/Ctty if already configured by SetupSocketConsole.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Cloneflags = cloneFlags

	if c.NS.Get("user").IsUserDefined && !c.NS.Get("pid").IsHost {
		cmd.SysProcAttr.UidMappings = []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: 0, Size: crt.ProcSize},
		}
		cmd.SysProcAttr.GidMappings = []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: 0, Size: crt.ProcSize},
		}
		// Only set process group for background containers without Setsid.
		// Setsid already creates a new session (and process group), so Setpgid
		// is redundant and conflicts (kernel returns EINVAL if both are set).
		if !cmd.SysProcAttr.Setsid {
			cmd.SysProcAttr.Setpgid = c.Background
		}
	}


	// Allocate a PTY for interactive containers when -t is passed
	// so the shell gets a real terminal (isatty=true, job control works).
	var restoreTerminal func()
	if !c.Background && c.TTY {
		master, slave, ptyErr := allocPTY()
		if ptyErr == nil {
			// Set PTY size from the host terminal
			if ws, wsErr := unix.IoctlGetWinsize(int(os.Stdin.Fd()), unix.TIOCGWINSZ); wsErr == nil {
				setPTYSize(master, ws.Row, ws.Col)
			} else {
				setPTYSize(master, 24, 80)
			}
			cmd.Stdin = slave
			cmd.Stdout = slave
			cmd.Stderr = slave
			cmd.SysProcAttr.Setsid = true
			cmd.SysProcAttr.Setctty = true
			cmd.SysProcAttr.Ctty = 0 // fd 0 = stdin = slave after dup2
			ptmx = master
			ptySlave = slave

			// Put host terminal in raw mode so keystrokes are forwarded
			// immediately (needed for programs like htop, vim, etc.)
			if oldTermios, rawErr := ioctlGetTermios(os.Stdin); rawErr == nil {
				setRawMode(os.Stdin, oldTermios)
				restoreTerminal = func() {
					unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TCSETS, oldTermios)
				}
			}
		} else {
			slog.Warn("PTY allocation failed, falling back to serial console", "error", ptyErr)
		}
	}

	if err := cmd.Start(); err != nil {
		if ptySlave != nil {
			ptySlave.Close()
		}
		if ptmx != nil {
			ptmx.Close()
		}
		if restoreTerminal != nil {
			restoreTerminal()
		}
		if consoleCleanup != nil {
			consoleCleanup()
		}
		return 1, fmt.Errorf("unable to start child process: %w", err)
	}

	c.ContPid = cmd.Process.Pid

	// Capture the kernel start-time so the recorded pid carries a stable
	// identity (defeats PID reuse), then persist pid+identity IMMEDIATELY —
	// before any provisioning below that can fail or be interrupted by daemon
	// shutdown. Otherwise that window leaves a live child the daemon doesn't
	// track, and the next health tick starts a duplicate (audit L1/L4).
	if st, stErr := crt.ProcessStartTime(c.ContPid); stErr == nil {
		c.ContPidStart = st
	} else {
		slog.Warn("crun: capture start-time", "pid", c.ContPid, "error", stErr)
	}
	c.Status = crt.ContainerStatusRunning
	if perr := controller.SetContainer(c); perr != nil {
		slog.Warn("crun: persist pid", "name", c.Name, "error", perr)
	}

	// Placement is committed: the pid+identity are published, so a concurrent
	// run/recover will now see this container as running. Drop the lifecycle
	// lock (if the caller passed its release here) so it isn't held across the
	// veth setup below or the long-lived foreground Wait — otherwise a
	// `sandal kill`/`stop`/`rm` of this container would block on the lock.
	if onPlaced != nil {
		onPlaced()
	}

	// childOwned flips true once the child is handed to the background wait
	// goroutine or reaped by the inline foreground Wait. Until then, any error
	// return must not leak the running child: the deferred guard kills+reaps it
	// and clears the persisted identity so no orphan survives and the recorded
	// pid never points at a dead/abandoned process (audit L1/L4).
	childOwned := false
	defer func() {
		if retErr != nil && !childOwned {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
			c.ContPid = 0
			c.ContPidStart = 0
			c.Status = fmt.Sprintf("err %v", retErr)
			controller.SetContainer(c)
		}
	}()

	// Close the slave PTY fd in the parent now that the child has inherited it.
	// This ensures when the child exits, all slave fds are closed and master
	// read returns EIO, allowing the relay goroutine to terminate.
	if ptySlave != nil {
		ptySlave.Close()
		ptySlave = nil
	}

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
	// (status was set to running and persisted right after cmd.Start above.)

	// Skip link creation when the container shares the host netns
	// (nothing to provision) or joins an existing one via --ns-net <target>
	// (the joined namespace is the user's; sandal must not mutate it).
	netConf := c.NS.Get("net")
	if !netConf.IsHost && !netConf.IsUserDefined {
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

	// Start port-forwarding for -p flags. A dedicated goroutine pinned to
	// one OS thread is setns'd into the container's network+mount namespaces
	// and serves net.Dial requests over a channel.
	//
	// The frontend differs by mode:
	//   - native:   host-side net.Listeners (tcp/unix/udp) bound in the host
	//               netns, dialing into the container via the NetnsDialer.
	//   - inside VM: AF_VSOCK listeners bound in the VM root netns, accepting
	//               connections from the physical host's BootWithForwards
	//               host listeners and dialing into the in-VM container.
	//
	// Lifecycle ownership differs by mode:
	//   - foreground (!c.Background): cleanup runs from this stack frame's
	//     defer, fired when cmd.Wait() returns at the bottom of crun.
	//   - background in daemon (c.Background && env.IsDaemon): the session
	//     is handed to host.Forwards. The wait goroutine below calls
	//     Forwards.Stop(c.Name) when the container exits.
	//   - background outside daemon: nothing can host the listeners past
	//     this function's return — log and skip.
	session, sErr := startForward(c)
	if sErr != nil {
		slog.Warn("forward: start", "err", sErr)
	}
	switch {
	case session == nil:
		// no -p flags or no daemon path; nothing to do.
	case !c.Background:
		defer session.close()
	case env.IsDaemon:
		Forwards.Add(c.Name, session)
	default:
		slog.Warn("forward: -p ignored for -d without daemon; use -startup or run in foreground", "name", c.Name)
		session.close()
	}

	// Start PTY relay for interactive (foreground) containers only.
	// Background containers with socket console have their own PTY reader.
	var stopRelay func()
	if ptmx != nil && !c.Background {
		stopRelay = startPTYRelay(ptmx, os.Stdin, os.Stdout)
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
				// Forward SIGWINCH to the PTY (resize), not to the process
				if sig == syscall.SIGWINCH && ptmx != nil {
					if ws, wsErr := unix.IoctlGetWinsize(int(os.Stdin.Fd()), unix.TIOCGWINSZ); wsErr == nil {
						setPTYSize(ptmx, ws.Row, ws.Col)
					}
					continue
				}

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
		// Capture the session pointer so the wait goroutine releases
		// exactly the session it created. Without this, a wait fired
		// after a contRecover-driven restart would clobber the new
		// session and orphan the old listener (port stuck "in use").
		ownedSession := session
		registered := env.IsDaemon && session != nil
		childOwned = true // the wait goroutine below now owns reaping
		go func() {
			cmd.Process.Wait()
			if registered {
				Forwards.Remove(c.Name, ownedSession)
			}
			if consoleCleanup != nil {
				consoleCleanup()
			}
			if ptmx != nil {
				ptmx.Close()
			}
		}()
		return 0, nil
	}

	sig, err := cmd.Process.Wait()
	childOwned = true // child reaped inline; no orphan for the deferred guard
	if err != nil && err.Error() == "waitid: no child processes" {
		err = nil
	}

	// Restore host terminal before any further output
	if restoreTerminal != nil {
		restoreTerminal()
	}

	// Clean up PTY relay after child exits
	if ptmx != nil {
		ptmx.Close()
		if stopRelay != nil {
			stopRelay()
		}
	}

	DeRunContainer(c)

	return sig.ExitCode(), err
}

func childEnv(c *config.Config, imageEnv []string) []string {
	if c.EnvAll {
		return appendSandalVariables(os.Environ(), c)
	}

	// Start with image ENV as the base layer. User/host overrides
	// take precedence and are applied on top.
	envVars := make([]string, 0, len(imageEnv)+len(c.PassEnv)+4)
	envVars = append(envVars, imageEnv...)

	// Track which variables the image already set.
	imageHas := make(map[string]bool, len(imageEnv))
	for _, e := range imageEnv {
		if name, _, ok := strings.Cut(e, "="); ok {
			imageHas[name] = true
		}
	}

	pathIsSet := imageHas["PATH"]
	for _, env := range c.PassEnv {
		if env == "PATH" {
			pathIsSet = true
		}
		variable := os.Getenv(env)
		if variable == "" {
			slog.Info("environment variable not found", "variable", env)
		} else {
			envVars = append(envVars, fmt.Sprintf("%s=%s", env, variable))
		}
	}
	if !pathIsSet {
		hostPath := os.Getenv("PATH")
		if hostPath == "" {
			hostPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
		}
		envVars = append(envVars, fmt.Sprintf("PATH=%s", hostPath))
	}

	// Always pass TERM when TTY is enabled so terminal programs
	// (htop, vim, etc.) know the terminal capabilities (arrow keys, mouse, colors).
	if c.TTY {
		term := os.Getenv("TERM")
		if term == "" {
			term = "xterm-256color"
		}
		envVars = append(envVars, "TERM="+term)
	}

	envVars = appendSandalVariables(envVars, c)
	return envVars
}

// ioctlGetTermios gets the current terminal settings.
func ioctlGetTermios(f *os.File) (*unix.Termios, error) {
	return unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
}

// setRawMode puts the terminal into raw mode for PTY relay.
func setRawMode(f *os.File, oldTermios *unix.Termios) {
	raw := *oldTermios
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	unix.IoctlSetTermios(int(f.Fd()), unix.TCSETS, &raw)
}

func appendSandalVariables(s []string, c *config.Config) []string {
	s = append(s, "SANDAL_CHILD"+"="+c.Name)
	for _, r := range env.GetDefaults() {
		s = append(s, r.Name+"="+r.Cur)
	}
	// Pass VM info so the child can detect VM mode and resolve VirtioFS mounts.
	// Do NOT pass SANDAL_VM_ARGS — it causes Main() to override os.Args and re-exec.
	if val := os.Getenv("SANDAL_VM"); val != "" {
		s = append(s, "SANDAL_VM="+val)
	}
	if val := os.Getenv("SANDAL_VM_MOUNTS"); val != "" {
		s = append(s, "SANDAL_VM_MOUNTS="+val)
	}
	return s
}
