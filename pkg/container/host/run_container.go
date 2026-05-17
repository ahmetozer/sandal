//go:build linux

package host

import (
	"fmt"
	"log/slog"
	gonet "net"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/net"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

// RunContainer is the canonical entry point shared by `sandal run` and
// `sandal build`: it validates, sets up networking, persists, and
// executes a container. The config should have Name, ContArgs, Volumes,
// Lower, NS, Capabilities, etc. already populated (e.g. from CLI flag
// parsing or API input). networkFlags are the raw -net flag values for
// network interface configuration.
//
// This used to live in pkg/sandal, but `sandal build` needs to call it
// too — and pkg/sandal imports pkg/container/build for the builder
// dispatch, so having build call pkg/sandal would create an import
// cycle. Moving it here (which pkg/sandal already imports) breaks the
// cycle without duplicating logic; pkg/sandal.RunContainer is now a
// thin wrapper around this function.
func RunContainer(c *config.Config, networkFlags []string) error {
	if err := config.ValidateName(c.Name); err != nil {
		return err
	}

	conts, err := controller.Containers()
	if err != nil {
		return fmt.Errorf("unable to get other container informations %s", err)
	}

	oldContStatus, err := crt.IsContainerRunning(c.Name)
	if err != nil {
		return err
	}

	if oldContStatus {
		return fmt.Errorf("container %s is already running", c.Name)
	}

	if c.Startup && !c.Background {
		return fmt.Errorf("startup only works with background mode, please enable with '-d' arg")
	}

	c.Net, err = net.ParseFlag(networkFlags, conts, c)
	if err != nil {
		return err
	}

	if err := c.NS.Defaults(); err != nil {
		return err
	}

	err = controller.SetContainer(c)
	if err != nil {
		return err
	}

	// Best-effort: drop any stale neighbor-cache entries on the bridge for
	// the IPs this container is about to claim. Otherwise a restart that
	// reuses the previous IPs can sit behind a STALE/FAILED entry pointing
	// at the dead veth's MAC until the kernel times it out.
	if links, lerr := net.ToLinks(&c.Net); lerr == nil && links != nil {
		var reusedIPs []gonet.IP
		for _, l := range *links {
			for _, a := range l.Addr {
				if a.IP != nil {
					reusedIPs = append(reusedIPs, a.IP)
				}
			}
		}
		if len(reusedIPs) > 0 {
			if err := net.FlushNeighForIPs(net.DefaultBridgeInterface, reusedIPs); err != nil {
				slog.Warn("flush stale neighbor cache failed", "iface", net.DefaultBridgeInterface, "err", err)
			}
		}
	}

	return Run(c)
}
