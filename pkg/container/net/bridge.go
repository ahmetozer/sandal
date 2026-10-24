//go:build linux

package net

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"log/slog"
	"net"
	"os"
	"strings"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/sysctl"
	"github.com/vishvananda/netlink"
)

const (
	DefaultBridgeInterface = "sandal0"
)

// Expected to run once
// In case of existence, returns error instead of nil to prevent
// any multi ip configuration at deamonless execution
func CreateDefaultBridge() (netlink.Link, error) {

	// VM mode: no bridge needed — eth0 is moved directly into the container netns.
	if isVM, _ := env.IsVM(); isVM {
		return nil, nil
	}

	masterlink, err := netlink.LinkByName(DefaultBridgeInterface)
	if err == nil {
		ensureBridgeMAC(masterlink)
		return masterlink, os.ErrExist
	}

	// A bridge created without an address takes the lowest port MAC and
	// recomputes it on every port change, while containers keep the old
	// one cached for their gateway. An explicit address is kept.
	masterlink = &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{
		Name:         DefaultBridgeInterface,
		HardwareAddr: defaultBridgeMAC(),
	}}

	err = linkAdd(masterlink)
	if errors.Is(err, syscall.EEXIST) {
		// Another run created it after our lookup; it sets it up.
		if existing, lerr := netlink.LinkByName(DefaultBridgeInterface); lerr == nil {
			ensureBridgeMAC(existing)
			return existing, os.ErrExist
		}
	}
	if err != nil {
		return nil, err
	}

	// Disable STP and set forward delay to 0 so new ports forward immediately.
	bridgeSysPath := sysClassNet + DefaultBridgeInterface + "/bridge/"
	os.WriteFile(bridgeSysPath+"stp_state", []byte("0"), 0644)
	os.WriteFile(bridgeSysPath+"forward_delay", []byte("0"), 0644)
	setBridgeNotify(DefaultBridgeInterface)

	err = netlink.LinkSetUp(masterlink)
	if err != nil {
		return nil, err
	}

	// Bare Linux: assign static IPs from SANDAL_HOST_NET. Only static
	// entries (no %uv6% token) are applied here — dynamic entries owe
	// their values to the live upstream prefix and are reconciled by the
	// renumber service on each prefix event.
	static, _ := SplitHostNet(env.DefaultHostNet)
	addrs, err := stringToAddrs(strings.Join(static, ","))
	if err != nil {
		return nil, err
	}
	err = addrs.Add(masterlink)
	if err != nil {
		return nil, err
	}

	return netlink.LinkByName(DefaultBridgeInterface)
}

// linkAdd is netlink.LinkAdd; tests replace it.
var linkAdd = netlink.LinkAdd

// sysClassNet is a variable so tests in a private netns can point it at
// their own sysfs mount.
var sysClassNet = "/sys/class/net/"

// NET_ADDR_* values of /sys/class/net/<if>/addr_assign_type.
const (
	netAddrRandom = "1" // generated: a bridge then follows its ports
	netAddrStolen = "2"
)

// ensureBridgeMAC fixes the MAC of a sandal0 created without one (by an
// older sandal). ndisc_notify and arp_notify are set first, so the kernel
// announces the new MAC (unsolicited NA, gratuitous ARP) and running
// containers update their gateway entries. A MAC set by the user or udev
// is kept.
func ensureBridgeMAC(link netlink.Link) {
	name := link.Attrs().Name
	setBridgeNotify(name)
	b, err := os.ReadFile(sysClassNet + name + "/addr_assign_type")
	if err != nil || !needsFixedMAC(string(b)) {
		return
	}
	mac := defaultBridgeMAC()
	if err := netlink.LinkSetHardwareAddr(link, mac); err != nil {
		slog.Warn("bridge: unable to fix MAC", "bridge", name, "mac", mac.String(), "err", err)
		return
	}
	slog.Info("bridge: MAC fixed", "bridge", name, "from", link.Attrs().HardwareAddr.String(), "to", mac.String())
}

// setBridgeNotify makes the kernel announce the bridge addresses whenever
// its MAC changes. Failures (e.g. IPv6 disabled) are not fatal.
func setBridgeNotify(name string) {
	_, _ = sysctl.Ensure("net.ipv6.conf."+name+".ndisc_notify", "1")
	_, _ = sysctl.Ensure("net.ipv4.conf."+name+".arp_notify", "1")
}

func needsFixedMAC(addrAssignType string) bool {
	t := strings.TrimSpace(addrAssignType)
	return t == netAddrRandom || t == netAddrStolen
}

// defaultBridgeMAC derives sandal0's MAC from the machine-id, or from the
// uplink MAC when there is no machine-id, or picks a random one when neither
// is known. sandal0 then keeps it (an explicitly set MAC is not replaced).
func defaultBridgeMAC() net.HardwareAddr {
	id := machineID()
	var uplink net.HardwareAddr
	if len(id) == 0 {
		name := env.UpstreamInterface
		if name == "" {
			name, _ = DetectUpstream()
		}
		if name != "" && name != DefaultBridgeInterface {
			if l, err := netlink.LinkByName(name); err == nil {
				uplink = l.Attrs().HardwareAddr
			}
		}
	}
	if mac := stableBridgeMAC(id, uplink); mac != nil {
		return mac
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return localUnicast(b)
}

// stableBridgeMAC hashes the machine-id into a locally administered unicast
// MAC, so it is the same on every boot of a host with a machine-id. The
// uplink MAC is the seed only without a machine-id: the daemon can create
// sandal0 before the uplink exists, so mixing it in would make the MAC
// depend on boot timing. Returns nil without a seed.
func stableBridgeMAC(machineID []byte, uplink net.HardwareAddr) net.HardwareAddr {
	if len(machineID) != 0 {
		uplink = nil
	}
	if len(machineID) == 0 && len(uplink) == 0 {
		return nil
	}
	h := sha256.New()
	h.Write([]byte("sandal0 mac\x00"))
	h.Write(machineID)
	h.Write([]byte{0})
	h.Write(uplink)
	return localUnicast(h.Sum(nil)[:6])
}

func localUnicast(b []byte) net.HardwareAddr {
	mac := make(net.HardwareAddr, 6)
	copy(mac, b)
	mac[0] = mac[0]&^0x01 | 0x02
	return mac
}

func machineID() []byte {
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if b, err := os.ReadFile(p); err == nil {
			if id := strings.TrimSpace(string(b)); id != "" {
				return []byte(id)
			}
		}
	}
	return nil
}
