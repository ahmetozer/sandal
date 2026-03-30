//go:build linux

package guest

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"

	"github.com/ahmetozer/sandal/pkg/container/net"
	cruntime "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// IsChild returns true when this process was spawned as a container child.
func IsChild() bool {
	return os.Getenv("SANDAL_CHILD") != ""
}

// Executed at namespace environment before real process
func ContainerInitProc() {
	goruntime.GOMAXPROCS(1)

	err := func() error {

		var (
			err error
			c   *config.Config
		)

		// The config is already written to disk by cmd/run.go before crun()
		// spawns this child process, so the retry loop is just a safety margin
		// for slow filesystems — not a race condition. The later SetContainer
		// in crun.go only updates runtime fields (PID, status) that the child
		// does not depend on.
		// Wait until main proccess resumes
		for retry := 1; retry < 10; retry++ {
			c, err = controller.GetContainer(os.Getenv("SANDAL_CHILD"))
			if err == nil {
				break
			}
			if retry > 5 {
				return fmt.Errorf("unable to load config: %s", err)
			}
			time.Sleep(time.Second / 10)
		}

		if len(c.ContArgs) < 1 {
			return fmt.Errorf("no executable is provided")
		}


		if err := unix.Sethostname([]byte(c.Name)); err != nil {
			return fmt.Errorf("unable to set hostname %s", err)
		}

		k, err := netlink.LinkByName("lo")
		if err == nil {
			netlink.LinkSetUp(k)
		}


		if !c.NS.Get("net").IsHost {

			links, err := net.ToLinks(&c.Net)
			if err != nil {
				return fmt.Errorf("net to links %s", err)
			}

			_, err = links.WaitUntilCreated(5)
			if err != nil {
				return err
			}

			for i := range *links {
				err = (*links)[i].Configure()
				if err != nil {
					return fmt.Errorf("interface error %s, %s", (*links)[i].Id, err)
				}

			}
			IPv4, IPv6 := links.FindGateways()
			_, b, _ := net.HasRoute(net.Ipv4DefaultGatewayTestIp())
			if !b && IPv4.IP != nil {
				err = netlink.RouteAdd(&netlink.Route{
					Dst: IPv4.IPNet,
					Gw:  IPv4.IP,
				})
				if err != nil {
					slog.Warn("unable to add gateway", "IPv4", IPv4, "err", err)
					// return err
				}
			}
			_, b, _ = net.HasRoute(net.Ipv6DefaultGatewayTestIp())
			if !b && IPv6.IP != nil {
				err = netlink.RouteAdd(&netlink.Route{
					Dst: IPv6.IPNet,
					Gw:  IPv6.IP,
				})
				if err != nil {
					slog.Warn("unable to add gateway", "IPv6", IPv6, "err", err)
					// return err
				}
			}

			err = links.FinalizeLinks()
			if err != nil {
				return err
			}
		}


		err = childSysMounts(c)
		if err != nil {
			return err
		}
		err = childSysNodes(c.Devtmpfs)
		if err != nil {
			return err
		}
		// Pre-pivot and pre-exec commands intentionally run as root before user
		// switching. This is by design: they are specified by the container operator
		// (not the container image) via -rcp/-rci flags and require root privileges
		// for tasks like device setup or filesystem preparation. The config file is
		// protected by root-only daemon socket permissions (0600).
		cruntime.RunCommands(c.RunPrePivot, "/.old_root/", "")
		if err := purgeOldRoot(c); err != nil {
			return err
		}
		cruntime.RunCommands(c.RunPreExec, "", "")


		if len(c.ContArgs) == 0 {
			return fmt.Errorf("no container arg provided, malformed container file")
		}
		execPath, err := exec.LookPath(c.ContArgs[0])
		slog.Debug("Exec", "c.Exec", c.ContArgs[0], "execPath", execPath, slog.Any("args", c.ContArgs[1:]))
		if err != nil {
			return fmt.Errorf("unable to find %s: %s", c.ContArgs[0], err)
		}

		var environ []string
		user, err := cruntime.GetUser(c.User)
		if err != nil {
			return err
		}

		if user.User != nil && user.User.HomeDir != "" {
			environ = []string{"HOME=" + user.User.HomeDir}
		}

		// Pass host environment but strip sandal's own internal
		// variables (SANDAL_CHILD, SANDAL_LIB_DIR, etc.) so they
		// don't leak into the container and interfere with nested
		// sandal invocations.  User-defined variables that happen
		// to start with SANDAL_ are preserved.
		internalVars := map[string]struct{}{
			"SANDAL_CHILD": {},
			"SANDAL_VM":    {},
			"SANDAL_VM_MOUNTS": {},
			"SANDAL_VM_ARGS":   {},
		}
		for _, d := range env.GetDefaults() {
			internalVars[d.Name] = struct{}{}
		}
		for _, e := range os.Environ() {
			name, _, _ := strings.Cut(e, "=")
			if _, internal := internalVars[name]; !internal {
				environ = append(environ, e)
			}
		}

		// Set system capabilities before switching user
		// This must be done while still running as root
		if err := c.Capabilities.Set(); err != nil {
			return fmt.Errorf("unable to set capabilities: %s", err)
		}

		err = switchUser(user)
		if err != nil {
			return err
		}

		// After switching from root to non-root user, restore effective capabilities
		// The kernel clears the effective set during user switch, even though
		// permitted capabilities are preserved via PR_SET_KEEPCAPS
		if err := c.Capabilities.RestoreEffective(); err != nil {
			return fmt.Errorf("unable to restore effective capabilities: %s", err)
		}

		if c.Dir != "" {
			os.Chdir(c.Dir)
		}

		// Jump to real process
		if err := unix.Exec(execPath, append([]string{c.ContArgs[0]}, c.ContArgs[1:]...), environ); err != nil {
			return fmt.Errorf("unable to exec %s: %s", c.ContArgs[0], err)
		}
		return nil
	}()

	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}

}
