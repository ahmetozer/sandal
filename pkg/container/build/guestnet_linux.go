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
	"golang.org/x/sys/unix"
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

	// Overlay a tmpfs on the guest-side temp dir. The default temp
	// path (env.BaseTempDir = /var/lib/sandal/tmp) lives under the
	// virtiofs-shared sandal-lib mount, and virtiofs does not support
	// symlink() or several other POSIX ops — which breaks OCI layer
	// extraction (many base images contain busybox-style symlinks).
	// A tmpfs overlay keeps the image cache (/var/lib/sandal/image)
	// on virtiofs (so the squashfs result reaches the host) while
	// extraction scratch space lives on a fully-featured local fs.
	if err := mountGuestTempTmpfs(); err != nil {
		slog.Warn("guest net: tmp tmpfs", "err", err)
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

	// Configure() doesn't install a default route for statically-
	// allocated links (KVM), so add one now using the bridge-pool
	// gateway that sandalnet pre-populated into Route. For DHCPv4,
	// the DHCP client already installed the default route.
	if !links[0].DHCPv4 && !links[0].DHCPv6 {
		for _, r := range links[0].Route {
			if r.IP == nil {
				continue
			}
			_ = netlink.RouteAdd(&netlink.Route{Gw: r.IP})
		}
	}
	slog.Info("guest net configured", slog.String("vm", os.Getenv("SANDAL_VM")))
	return nil
}

// mountGuestTempTmpfs tmpfs-mounts env.BaseTempDir so OCI layer
// extraction (which needs symlink() support) works inside a VM guest
// whose /var/lib/sandal is virtiofs-backed.
func mountGuestTempTmpfs() error {
	if err := os.MkdirAll(env.BaseTempDir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", env.BaseTempDir, err)
	}
	// Idempotency: if it's already a tmpfs, skip.
	var st unix.Statfs_t
	if err := unix.Statfs(env.BaseTempDir, &st); err == nil {
		// TMPFS_MAGIC = 0x01021994
		if st.Type == 0x01021994 {
			return nil
		}
	}
	if err := unix.Mount("tmpfs", env.BaseTempDir, "tmpfs", 0, ""); err != nil {
		return fmt.Errorf("tmpfs mount: %w", err)
	}
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
