//go:build linux

package build

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/ahmetozer/sandal/pkg/container/config"
	sandalnet "github.com/ahmetozer/sandal/pkg/container/net"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/vishvananda/netlink"
)

// EnsureGuestNet configures eth0 on the VM host netns so the build
// process (which runs OUTSIDE any container) can reach OCI registries
// for FROM pulls and COPY --from=<image> fetches.
//
// `sandal run -vm` does not need this: it moves eth0 into a container
// netns and lets the container network path configure it there. But
// build pulls images in the sandal process itself, before any
// container exists — so we have to configure eth0 at the VM host level.
//
// We reuse sandalnet.ParseFlag, whose link.defaults() already knows how
// to build the right Link for each VM type:
//
//   - KVM: copies static Addr/Route from SANDAL_VM_NET (host-allocated).
//   - VZ : sets DHCPv4=true so Configure() runs the DHCP client.
//
// Then Link.Configure() assigns the addresses and (for VZ) runs DHCP —
// exactly the same code that configures eth0 for `sandal run` containers.
//
// No-op outside a VM, or when eth0 already has an address.
func EnsureGuestNet(ctx context.Context) error {
	if isVM, _ := env.IsVM(); !isVM {
		return nil
	}

	// Make host CA certs visible so Go's TLS stack can verify OCI
	// registry certificates. VMInit chroots into an empty tmpfs and
	// only populates /etc/resolv.conf; /etc/ssl would otherwise be
	// missing and FROM pulls over HTTPS fail with "unknown authority".
	if err := exposeHostCACerts(); err != nil {
		slog.Warn("guest net: expose CA certs", "err", err)
	}

	link, err := netlink.LinkByName("eth0")
	if err != nil {
		return fmt.Errorf("guest net: eth0: %w", err)
	}
	// Already configured? Nothing to do.
	if addrs, _ := netlink.AddrList(link, netlink.FAMILY_V4); len(addrs) > 0 {
		return nil
	}

	// Reuse sandalnet.ParseFlag so we go through the exact same
	// link.defaults() path as `sandal run` containers. Inside a VM
	// that produces a passthru Link pre-filled with KVM static
	// addresses or VZ DHCPv4=true.
	synthCfg := &config.Config{Name: "sandal-build-guestnet"}
	links, err := sandalnet.ParseFlag(nil, nil, synthCfg)
	if err != nil {
		return fmt.Errorf("guest net: parse link: %w", err)
	}
	if len(links) == 0 {
		return fmt.Errorf("guest net: no link produced")
	}

	// Apply IP (+ DHCP if VZ) to the VM host's eth0 — same helper
	// sandal run uses to configure eth0 inside container netns.
	if err := links[0].Configure(); err != nil {
		return fmt.Errorf("guest net: configure eth0: %w", err)
	}

	// Configure() assigns IPs (and runs the DHCP client for VZ), but
	// neither path installs a kernel default route. Install one now
	// from the Route entries that defaults() (KVM static) or
	// configureDHCPv4() (VZ lease) populated.
	for _, r := range links[0].Route {
		if r.IP == nil {
			continue
		}
		_ = netlink.RouteAdd(&netlink.Route{Gw: r.IP})
	}
	slog.Info("guest net configured", slog.String("vm", os.Getenv("SANDAL_VM")))
	return nil
}

// exposeHostCACerts symlinks CA-cert directories from the
// /mnt/host-etc virtiofs share into the guest's /etc so Go's TLS
// stack can find them at the standard search locations.
func exposeHostCACerts() error {
	candidates := []string{
		"/etc/ssl",
		"/etc/pki",
	}
	for _, p := range candidates {
		src := "/mnt/host-etc" + strings.TrimPrefix(p, "/etc")
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if _, err := os.Lstat(p); err == nil {
			continue
		}
		if err := os.MkdirAll("/etc", 0755); err != nil {
			return err
		}
		if err := os.Symlink(src, p); err != nil && !os.IsExist(err) {
			return fmt.Errorf("symlink %s → %s: %w", p, src, err)
		}
	}
	return nil
}
