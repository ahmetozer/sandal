//go:build linux

package guest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sandalnet "github.com/ahmetozer/sandal/pkg/container/net"
	cmount "github.com/ahmetozer/sandal/pkg/container/mount"
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
	// Always parse kernel cmdline to decode base64-encoded env vars.
	// On VZ, the kernel auto-exports cmdline params but keeps them
	// base64-encoded. On KVM, they aren't exported at all.
	importKernelCmdlineEnv()
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
		cmount.Mount("proc", "/proc", "proc", 0, "")
	}
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return
	}
	cmdline := strings.TrimSpace(string(data))

	// Parse space-separated tokens. Handle quoted values and
	// SANDAL_VM_ARGS which contains JSON with potential spaces inside brackets.
	// Keys that are base64-encoded on the kernel cmdline to survive parsing.
	// On VZ, the kernel auto-exports cmdline params as env vars but keeps
	// them encoded. Always decode and re-set these even if already present.
	b64Keys := map[string]bool{
		"SANDAL_VM_ARGS": true,
		"SANDAL_VM_NET":  true,
	}

	for _, param := range parseCmdlineParams(cmdline) {
		if idx := strings.IndexByte(param, '='); idx > 0 {
			key := param[:idx]
			val := param[idx+1:]
			if b64Keys[key] {
				if decoded, err := base64.StdEncoding.DecodeString(val); err == nil {
					val = string(decoded)
				}
				os.Setenv(key, val)
			} else if os.Getenv(key) == "" {
				os.Setenv(key, val)
			}
		}
	}

	// Override os.Args with the decoded SANDAL_VM_ARGS so cmd.Main()
	// dispatches the correct subcommand on both KVM and VZ.
	if vmArgs := os.Getenv("SANDAL_VM_ARGS"); vmArgs != "" {
		var args []string
		if err := json.Unmarshal([]byte(vmArgs), &args); err == nil && len(args) > 0 {
			os.Args = append([]string{os.Args[0]}, args...)
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
	cmount.Mount("proc", "/proc", "proc", 0, "")

	os.MkdirAll("/dev", 0755)
	cmount.Mount("devtmpfs", "/dev", "devtmpfs", 0, "")

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
	if err := cmount.Mount("sysfs", "/sys", "sysfs", 0, ""); err != nil {
		return fmt.Errorf("mount /sys: %w", err)
	}

	os.MkdirAll("/dev", 0755)
	cmount.Mount("devtmpfs", "/dev", "devtmpfs", 0, "")

	// Load kernel modules before switch_root (modules live in the base initrd).
	// virtio_mmio must be loaded first — it creates the virtio bus devices
	// that virtiofs, virtio_net, and virtio_blk bind to.
	for _, mod := range []string{
		// Virtio transport (must come before device drivers)
		"virtio_mmio",
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
		// Vsock (host<->guest socket communication)
		"vsock", "vmw_vsock_virtio_transport_common", "vmw_vsock_virtio_transport",
	} {
		if err := modprobe.Load(mod); err != nil {
			slog.Warn("modprobe", slog.String("module", mod), slog.Any("err", err))
		}
	}

	// The kernel's initramfs root (rootfs) doesn't support pivot_root.
	os.MkdirAll("/newroot", 0755)
	if err := cmount.Mount("tmpfs", "/newroot", "tmpfs", 0, ""); err != nil {
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
	cmount.Mount("proc", "/newroot/proc", "proc", 0, "")
	cmount.Mount("sysfs", "/newroot/sys", "sysfs", 0, "")
	cmount.Mount("devtmpfs", "/newroot/dev", "devtmpfs", 0, "")
	os.MkdirAll("/newroot/dev/pts", 0755)
	cmount.Mount("devpts", "/newroot/dev/pts", "devpts", 0, "gid=5,mode=620,ptmxmode=666")

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
			slog.Warn("invalid mount spec", slog.String("entry", entry))
			continue
		}
		tag := parts[0]
		hostPath := parts[1]
		mountPoint := "/mnt" + hostPath
		if len(parts) == 3 && parts[2] != "" {
			mountPoint = parts[2]
		}
		os.MkdirAll(mountPoint, 0755)
		// Retry briefly — the virtiofs driver may still be probing the device
		// after module load, especially when virtio_mmio is loaded as a module.
		var mountErr error
		for attempt := 0; attempt < 50; attempt++ {
			mountErr = cmount.Mount(tag, mountPoint, "virtiofs", 0, "")
			if mountErr == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if mountErr != nil {
			slog.Warn("failed to mount virtiofs", slog.String("tag", tag), slog.String("mountpoint", mountPoint), slog.Any("err", mountErr))
		}
	}

	// Start vsock socket relay for SANDAL_VM_SOCKETS
	if sockSpec := os.Getenv("SANDAL_VM_SOCKETS"); sockSpec != "" {
		startGuestSocketRelay(sockSpec)
	}

	// Configure guest network from SANDAL_VM_NET (JSON-encoded Link from host).
	// The host allocated an IP from the sandal0 bridge subnet and passed it here.
	if netSpec := os.Getenv("SANDAL_VM_NET"); netSpec != "" {
		if err := vmConfigureNetwork(netSpec); err != nil {
			slog.Warn("guest network setup failed", slog.Any("err", err))
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
			slog.Warn("failed to add address", slog.String("ip", addr.IP.String()), slog.String("iface", ifName), slog.Any("err", err))
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

	slog.Info("network configured", slog.String("iface", ifName), slog.Any("addr", link.Addr))
	return nil
}

// startGuestSocketRelay parses SANDAL_VM_SOCKETS (format: port=hostpath=guestpath,...)
// and starts a background goroutine for each socket that relays connections
// from the guest Unix socket to the host via vsock.
func startGuestSocketRelay(spec string) {
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "=", 3)
		if len(parts) < 3 {
			slog.Warn("invalid socket spec", slog.String("entry", entry))
			continue
		}
		port, err := strconv.ParseUint(parts[0], 10, 32)
		if err != nil {
			slog.Warn("invalid socket port", slog.String("entry", entry), slog.Any("err", err))
			continue
		}
		guestPath := parts[2]

		// Create relay socket under /var/run/sandal/vsock/ so it survives
		// the container's pivot_root (the container rewrites -v to source from here).
		relayPath := "/var/run/sandal/vsock" + guestPath
		os.MkdirAll(filepath.Dir(relayPath), 0755)
		os.Remove(relayPath)

		go guestRelaySocket(relayPath, uint32(port))
	}
}

// guestRelaySocket listens on a Unix socket at guestPath and for each
// accepted connection, dials AF_VSOCK to the host (CID=2) on the given port,
// then performs bidirectional relay.
func guestRelaySocket(guestPath string, port uint32) {
	ln, err := net.Listen("unix", guestPath)
	if err != nil {
		slog.Warn("guest socket listen failed", slog.String("path", guestPath), slog.Any("err", err))
		return
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			slog.Warn("guest socket accept failed", slog.String("path", guestPath), slog.Any("err", err))
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			// CID 2 is the host
			vsock, err := vsockDial(2, port)
			if err != nil {
				slog.Warn("vsock dial failed", slog.Uint64("port", uint64(port)), slog.Any("err", err))
				return
			}
			defer vsock.Close()

			done := make(chan struct{})
			go func() {
				io.Copy(vsock, c) // *os.File implements io.Writer
				done <- struct{}{}
			}()
			io.Copy(c, vsock) // *os.File implements io.Reader
			<-done
		}(conn)
	}
}

// vsockDial creates an AF_VSOCK connection to the given CID and port.
// Returns an *os.File because Go's net package doesn't support AF_VSOCK.
func vsockDial(cid, port uint32) (*os.File, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("vsock socket: %w", err)
	}
	err = unix.Connect(fd, &unix.SockaddrVM{CID: cid, Port: port})
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("vsock connect: %w", err)
	}
	return os.NewFile(uintptr(fd), fmt.Sprintf("vsock:%d:%d", cid, port)), nil
}

// ControllerVsockPort is the well-known vsock port for the embedded controller API.
const ControllerVsockPort = 4000

// ControllerSocketPath is the Unix socket where the embedded controller listens inside the VM.
const ControllerSocketPath = "/var/run/sandal/controller.sock"

// StartControllerVsockListener listens on vsock port 4000 and relays each
// connection to the embedded controller Unix socket inside the VM.
// Must be called as a goroutine — blocks forever.
func StartControllerVsockListener() {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		slog.Warn("controller vsock: socket", slog.Any("err", err))
		return
	}

	err = unix.Bind(fd, &unix.SockaddrVM{
		CID:  unix.VMADDR_CID_ANY,
		Port: ControllerVsockPort,
	})
	if err != nil {
		unix.Close(fd)
		slog.Warn("controller vsock: bind", slog.Any("err", err))
		return
	}

	err = unix.Listen(fd, 8)
	if err != nil {
		unix.Close(fd)
		slog.Warn("controller vsock: listen", slog.Any("err", err))
		return
	}

	slog.Info("controller vsock listener ready", slog.Uint64("port", ControllerVsockPort))

	for {
		nfd, _, err := unix.Accept(fd)
		if err != nil {
			slog.Warn("controller vsock: accept", slog.Any("err", err))
			continue
		}
		go controllerVsockRelay(nfd)
	}
}

// readerOnly hides WriteTo so io.Copy can't use splice/sendfile.
type readerOnly struct{ io.Reader }

// writerOnly hides ReadFrom so io.Copy can't use splice/sendfile.
type writerOnly struct{ io.Writer }

// controllerVsockRelay relays a single vsock connection to the embedded
// controller Unix socket. When one direction finishes (e.g., exec output
// complete), connections are closed to unblock the other direction.
func controllerVsockRelay(vsockFD int) {
	vsockFile := os.NewFile(uintptr(vsockFD), "vsock-controller")

	ctrlConn, err := net.Dial("unix", ControllerSocketPath)
	if err != nil {
		slog.Warn("controller dial failed", slog.Any("err", err))
		vsockFile.Close()
		return
	}

	// Wrap to prevent splice/sendfile which can batch interactive keystrokes.
	// host→controller (request + stdin)
	go func() {
		io.Copy(writerOnly{ctrlConn}, readerOnly{vsockFile})
		ctrlConn.Close()
	}()
	// controller→host (response + stdout)
	io.Copy(writerOnly{vsockFile}, readerOnly{ctrlConn})
	vsockFile.Close()
}
