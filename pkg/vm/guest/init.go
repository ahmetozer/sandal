//go:build linux

package guest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	sandalnet "github.com/ahmetozer/sandal/pkg/container/cruntime/net"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/modprobe"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// IsVMInit returns true if sandal is running as PID 1 inside a VM.
// Detected by SANDAL_VM_ARGS env var, or by being PID 1 with /init as binary
// (kernel cmdline params aren't always exported as env vars on initramfs).
func IsVMInit() bool {
	if os.Getpid() != 1 {
		return false
	}
	if os.Getenv("SANDAL_VM_ARGS") != "" {
		return true
	}
	// On KVM, kernel cmdline params aren't auto-exported as env vars
	// for initramfs init. Parse /proc/cmdline to check and export them.
	importKernelCmdlineEnv()
	// Even if SANDAL_VM_ARGS is not set, if we're /init or /sandal-init at PID 1
	// we're likely running as VM init
	return os.Getenv("SANDAL_VM_ARGS") != "" ||
		os.Args[0] == "/init" || os.Args[0] == "/sandal-init"
}

// importKernelCmdlineEnv reads /proc/cmdline and sets KEY=VALUE pairs
// as environment variables. This is needed for KVM initramfs boot where
// the kernel doesn't auto-export cmdline params to init's environment.
func importKernelCmdlineEnv() {
	// /proc may not be mounted yet when running as PID 1 in initramfs.
	// Mount it and leave it mounted — VMInit() will use it later.
	if _, err := os.Stat("/proc/cmdline"); err != nil {
		os.MkdirAll("/proc", 0755)
		unix.Mount("proc", "/proc", "proc", 0, "")
	}
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return
	}
	cmdline := strings.TrimSpace(string(data))

	// Parse space-separated tokens. Handle quoted values and
	// SANDAL_VM_ARGS which contains JSON with potential spaces inside brackets.
	for _, param := range parseCmdlineParams(cmdline) {
		if idx := strings.IndexByte(param, '='); idx > 0 {
			key := param[:idx]
			val := param[idx+1:]
			// SANDAL_VM_ARGS and SANDAL_VM_NET are base64-encoded to survive kernel cmdline parsing
			if key == "SANDAL_VM_ARGS" || key == "SANDAL_VM_NET" {
				if decoded, err := base64.StdEncoding.DecodeString(val); err == nil {
					val = string(decoded)
				}
			}
			if os.Getenv(key) == "" {
				os.Setenv(key, val)
			}
		}
	}
}

// parseCmdlineParams splits a kernel command line into parameters,
// respecting bracket-enclosed JSON values (e.g., SANDAL_VM_ARGS=[...]).
func parseCmdlineParams(cmdline string) []string {
	var params []string
	var current strings.Builder
	depth := 0 // bracket nesting depth

	for i := 0; i < len(cmdline); i++ {
		c := cmdline[i]
		switch {
		case c == '[':
			depth++
			current.WriteByte(c)
		case c == ']':
			if depth > 0 {
				depth--
			}
			current.WriteByte(c)
		case c == ' ' && depth == 0:
			if current.Len() > 0 {
				params = append(params, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		params = append(params, current.String())
	}
	return params
}

// VMInit performs early system setup when sandal runs as PID 1 (init) inside a VM.
// It mounts essential filesystems, switches from initramfs rootfs to a real tmpfs
// (so the container runtime can later pivot_root), loads virtiofs modules, and
// mounts virtiofs shares.
func VMInit() error {
	// Mount essential filesystems on the initramfs (may already be mounted by preinit)
	os.MkdirAll("/proc", 0755)
	unix.Mount("proc", "/proc", "proc", 0, "")

	os.MkdirAll("/dev", 0755)
	unix.Mount("devtmpfs", "/dev", "devtmpfs", 0, "")

	// Redirect stdio to the console device so init output is visible
	if console, err := os.OpenFile("/dev/console", os.O_RDWR, 0); err == nil {
		unix.Dup2(int(console.Fd()), 0)
		unix.Dup2(int(console.Fd()), 1)
		unix.Dup2(int(console.Fd()), 2)
		if console.Fd() > 2 {
			console.Close()
		}
	}

	os.MkdirAll("/sys", 0755)
	if err := unix.Mount("sysfs", "/sys", "sysfs", 0, ""); err != nil {
		return fmt.Errorf("mount /sys: %w", err)
	}

	os.MkdirAll("/dev", 0755)
	unix.Mount("devtmpfs", "/dev", "devtmpfs", 0, "")

	// Load kernel modules before switch_root (modules live in the base initrd).
	for _, mod := range []string{
		// Filesystems
		"fuse", "virtiofs", "overlay", "loop", "squashfs", "ext4",
		// Networking
		"veth", "bridge", "tun", "af_packet",
		// Netfilter / NAT
		"nf_conntrack", "nf_nat", "nf_tables",
		"ip_tables", "iptable_nat", "iptable_filter",
		"ip6_tables", "ip6table_nat", "ip6table_filter",
		// IPVS
		"ip_vs", "ip_vs_rr", "ip_vs_wrr", "ip_vs_sh",
		// Overlay networking
		"vxlan", "geneve", "ipvlan", "macvlan",
		// Block / virtio
		"virtio_net", "virtio_blk",
	} {
		if err := modprobe.Load(mod); err != nil {
			fmt.Fprintf(os.Stderr, "modprobe %s: %v\n", mod, err)
		}
	}

	// The kernel's initramfs root (rootfs) doesn't support pivot_root.
	// Use switch_root approach: mount tmpfs, chroot into it.
	os.MkdirAll("/newroot", 0755)
	if err := unix.Mount("tmpfs", "/newroot", "tmpfs", 0, ""); err != nil {
		return fmt.Errorf("mount tmpfs on /newroot: %w", err)
	}

	// Copy init binary to the new root.
	// The binary may be /sandal-init (KVM with preinit) or /init (VZ).
	initSrc := "/init"
	if _, err := os.Stat("/sandal-init"); err == nil {
		initSrc = "/sandal-init"
	}
	initData, err := os.ReadFile(initSrc)
	if err != nil {
		return fmt.Errorf("reading %s: %w", initSrc, err)
	}
	if err := os.WriteFile("/newroot/init", initData, 0755); err != nil {
		return fmt.Errorf("writing /newroot/init: %w", err)
	}

	// Create directories in the new root
	for _, dir := range []string{"/proc", "/sys", "/dev", "/mnt", "/var/lib/sandal", "/var/run/sandal", "/tmp", "/etc"} {
		os.MkdirAll("/newroot"+dir, 0755)
	}

	// Mount proc/sys/dev/devpts in the new root
	unix.Mount("proc", "/newroot/proc", "proc", 0, "")
	unix.Mount("sysfs", "/newroot/sys", "sysfs", 0, "")
	unix.Mount("devtmpfs", "/newroot/dev", "devtmpfs", 0, "")
	os.MkdirAll("/newroot/dev/pts", 0755)
	unix.Mount("devpts", "/newroot/dev/pts", "devpts", 0, "gid=5,mode=620,ptmxmode=666")

	// Chroot into the new tmpfs root (rootfs doesn't support pivot_root)
	if err := unix.Chroot("/newroot"); err != nil {
		return fmt.Errorf("chroot /newroot: %w", err)
	}
	os.Chdir("/")

	// Update BinLoc to use the binary in the new root
	env.BinLoc = "/init"

	// Mount virtiofs shares from SANDAL_VM_MOUNTS (format: tag=hostpath,tag=hostpath)
	// Each share is mounted at /mnt/<hostpath> to mirror the host filesystem layout.
	mountSpec := os.Getenv("SANDAL_VM_MOUNTS")
	if mountSpec == "" {
		return nil
	}

	// Mount spec format: tag=hostpath or tag=hostpath=guestpath
	// Without guestpath, the share is mounted at /mnt/<hostpath>.
	for _, entry := range strings.Split(mountSpec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "=", 3)
		if len(parts) < 2 {
			fmt.Fprintf(os.Stderr, "Warning: invalid mount spec %q\n", entry)
			continue
		}
		tag := parts[0]
		hostPath := parts[1]
		mountPoint := "/mnt" + hostPath
		if len(parts) == 3 && parts[2] != "" {
			mountPoint = parts[2]
		}
		os.MkdirAll(mountPoint, 0755)
		if err := unix.Mount(tag, mountPoint, "virtiofs", 0, ""); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to mount virtiofs %s at %s: %v\n", tag, mountPoint, err)
		}
	}

	// Configure guest network from SANDAL_VM_NET (JSON-encoded Link from host).
	// The host allocated an IP from the sandal0 bridge subnet and passed it here.
	if netSpec := os.Getenv("SANDAL_VM_NET"); netSpec != "" {
		if err := vmConfigureNetwork(netSpec); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: guest network setup failed: %v\n", err)
		}
	}

	return nil
}

// vmConfigureNetwork configures the guest network interface from a JSON-encoded
// sandalnet.Link. The host allocated an IP and gateway from the sandal0 bridge.
func vmConfigureNetwork(netSpec string) error {
	var link sandalnet.Link
	if err := json.Unmarshal([]byte(netSpec), &link); err != nil {
		return fmt.Errorf("parsing SANDAL_VM_NET: %w", err)
	}

	// Wait for the virtio-net interface to appear (loaded via virtio_net module).
	// The interface may take a moment to register after modprobe.
	var nLink netlink.Link
	ifName := link.Name
	if ifName == "" {
		ifName = "eth0"
	}
	for i := 0; i < 50; i++ {
		var err error
		nLink, err = netlink.LinkByName(ifName)
		if err == nil {
			break
		}
		// Also try finding any virtio net interface
		links, _ := netlink.LinkList()
		for _, l := range links {
			if l.Attrs().Name != "lo" && l.Type() == "device" {
				nLink = l
				ifName = l.Attrs().Name
				break
			}
		}
		if nLink != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if nLink == nil {
		return fmt.Errorf("network interface %s not found", ifName)
	}

	// Assign IP addresses
	for _, addr := range link.Addr {
		nlAddr := &netlink.Addr{
			IPNet: &net.IPNet{
				IP:   addr.IP,
				Mask: addr.IPNet.Mask,
			},
		}
		if err := netlink.AddrAdd(nLink, nlAddr); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to add %s to %s: %v\n", addr.IP, ifName, err)
		}
	}

	// Bring interface up
	if err := netlink.LinkSetUp(nLink); err != nil {
		return fmt.Errorf("bringing up %s: %w", ifName, err)
	}

	// Set default routes from gateway (same pattern as init.go)
	gw4, gw6 := sandalnet.Links{link}.FindGateways()
	if gw4.IP != nil {
		netlink.RouteAdd(&netlink.Route{
			LinkIndex: nLink.Attrs().Index,
			Gw:        gw4.IP,
			Dst:       gw4.IPNet,
		})
	}
	if gw6.IP != nil {
		netlink.RouteAdd(&netlink.Route{
			LinkIndex: nLink.Attrs().Index,
			Gw:        gw6.IP,
			Dst:       gw6.IPNet,
		})
	}

	fmt.Fprintf(os.Stderr, "Network: %s configured with %v\n", ifName, link.Addr)
	return nil
}
