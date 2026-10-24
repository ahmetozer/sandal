//go:build linux

package net

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

// setupBridgeLab locks the test goroutine's thread into a fresh netns and
// mounts a sysfs of that netns, so sandal0 here is not the host's.
func setupBridgeLab(t *testing.T) {
	t.Helper()
	if os.Getuid() != 0 {
		t.Skip("e2e: requires root")
	}
	runtime.LockOSThread()
	orig, err := netns.Get()
	if err != nil {
		t.Fatal(err)
	}
	ns, err := netns.New()
	if err != nil {
		orig.Close()
		t.Skipf("e2e: cannot create netns: %v", err)
	}
	t.Cleanup(func() {
		err := netns.Set(orig)
		ns.Close()
		orig.Close()
		if err != nil {
			t.Errorf("restore netns: %v", err)
			return // keep the thread locked; it is discarded with the goroutine
		}
		runtime.UnlockOSThread()
	})

	// /sys shows the netns of its mounter, not of this thread.
	sys := t.TempDir()
	if err := unix.Mount("sysfs", sys, "sysfs", 0, ""); err != nil {
		t.Skipf("e2e: cannot mount sysfs: %v", err)
	}
	t.Cleanup(func() { _ = unix.Unmount(sys, unix.MNT_DETACH) })
	origSys, origHostNet, origIDs := sysClassNet, env.DefaultHostNet, machineIDFiles
	sysClassNet = sys + "/class/net/"
	env.DefaultHostNet = "192.0.2.1/24,fd00:db8:5::1/64"
	// A fixed machine-id, so the derived MAC does not depend on the test
	// host having one (without it the MAC is random on every call).
	idFile := t.TempDir() + "/machine-id"
	if err := os.WriteFile(idFile, []byte(labMachineID+"\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	machineIDFiles = []string{idFile}
	t.Cleanup(func() { sysClassNet, env.DefaultHostNet, machineIDFiles = origSys, origHostNet, origIDs })
}

// labMachineID is the machine-id setupBridgeLab gives the code under test.
const labMachineID = "0123456789abcdef0123456789abcdef"

// labBridgeMAC is the MAC sandal0 must get from labMachineID.
func labBridgeMAC() string {
	return stableBridgeMAC([]byte(labMachineID), nil).String()
}

func bridgeAssignType(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(sysClassNet + DefaultBridgeInterface + "/addr_assign_type")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func bridgeMAC(t *testing.T) string {
	t.Helper()
	l, err := netlink.LinkByName(DefaultBridgeInterface)
	if err != nil {
		t.Fatal(err)
	}
	return l.Attrs().HardwareAddr.String()
}

// churnLowPort attaches a port whose MAC is lower than any other and
// removes it again, which moves the MAC of a bridge without a fixed one.
func churnLowPort(t *testing.T, br netlink.Link) (during string) {
	t.Helper()
	low := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: "brmacA", HardwareAddr: []byte{0x02, 0, 0, 0, 0, 0x01}},
		PeerName:  "brmacB",
	}
	if err := netlink.LinkAdd(low); err != nil {
		t.Fatal(err)
	}
	if err := netlink.LinkSetMaster(low, br); err != nil {
		t.Fatal(err)
	}
	during = bridgeMAC(t)
	if err := netlink.LinkDel(low); err != nil {
		t.Fatal(err)
	}
	return during
}

func readSysctl(t *testing.T, key string) string {
	t.Helper()
	b, err := os.ReadFile("/proc/sys/" + strings.ReplaceAll(key, ".", "/"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestDefaultBridge_E2E_MACFixedAcrossPortChurn(t *testing.T) {
	setupBridgeLab(t)

	br, err := CreateDefaultBridge()
	if err != nil {
		t.Fatalf("CreateDefaultBridge: %v", err)
	}
	if got := bridgeAssignType(t); got != "3" {
		t.Errorf("addr_assign_type = %s, want 3 (set)", got)
	}
	before := bridgeMAC(t)
	if want := labBridgeMAC(); before != want {
		t.Errorf("mac = %s, want the derived %s", before, want)
	}
	if during := churnLowPort(t, br); during != before {
		t.Errorf("mac moved to %s while a lower port was attached (was %s)", during, before)
	}
	if after := bridgeMAC(t); after != before {
		t.Errorf("mac = %s after churn, want %s", after, before)
	}
	for _, k := range []string{"net.ipv6.conf.sandal0.ndisc_notify", "net.ipv4.conf.sandal0.arp_notify"} {
		if got := readSysctl(t, k); got != "1" {
			t.Errorf("%s = %s, want 1", k, got)
		}
	}
}

// A sandal0 left by an older sandal (no fixed MAC) is pinned when sandal
// next looks it up, and then stops following port churn.
func TestDefaultBridge_E2E_PinsExistingBridgeMAC(t *testing.T) {
	setupBridgeLab(t)

	old := &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: DefaultBridgeInterface}}
	if err := netlink.LinkAdd(old); err != nil {
		t.Fatal(err)
	}
	if got := bridgeAssignType(t); got == "3" {
		t.Skip("kernel sets bridge MACs explicitly already")
	}
	churnLowPort(t, old)

	br, err := CreateDefaultBridge()
	if err != os.ErrExist {
		t.Fatalf("CreateDefaultBridge err = %v, want os.ErrExist", err)
	}
	if got := bridgeAssignType(t); got != "3" {
		t.Errorf("addr_assign_type = %s, want 3 (set)", got)
	}
	pinned := bridgeMAC(t)
	if want := labBridgeMAC(); pinned != want {
		t.Errorf("mac = %s, want the derived %s", pinned, want)
	}
	churnLowPort(t, br)
	if after := bridgeMAC(t); after != pinned {
		t.Errorf("mac = %s after churn, want %s", after, pinned)
	}
	for _, k := range []string{"net.ipv6.conf.sandal0.ndisc_notify", "net.ipv4.conf.sandal0.arp_notify"} {
		if got := readSysctl(t, k); got != "1" {
			t.Errorf("%s = %s, want 1", k, got)
		}
	}

	// A MAC the user (or udev) set is left alone.
	user := []byte{0x02, 0xdb, 0x08, 0, 0, 0x05}
	if err := netlink.LinkSetHardwareAddr(br, user); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateDefaultBridge(); err != os.ErrExist {
		t.Fatalf("CreateDefaultBridge err = %v, want os.ErrExist", err)
	}
	if got := bridgeMAC(t); got != "02:db:08:00:00:05" {
		t.Errorf("mac = %s, want the user's 02:db:08:00:00:05 kept", got)
	}
}

// A veth whose master is sandal0 is attached to it also when creating the
// veth is what creates sandal0.
func TestCreateVeth_E2E_AttachesToNewDefaultBridge(t *testing.T) {
	setupBridgeLab(t)
	if _, err := netlink.LinkByName(DefaultBridgeInterface); err == nil {
		t.Fatal("sandal0 exists in a fresh netns")
	}

	l := Link{Id: "exveth0", Type: LinkTypeVeth, Master: DefaultBridgeInterface}
	if err := l.createVeth(); err != nil {
		t.Fatalf("createVeth: %v", err)
	}
	br, err := netlink.LinkByName(DefaultBridgeInterface)
	if err != nil {
		t.Fatalf("sandal0 not created: %v", err)
	}
	host, err := netlink.LinkByName("s-" + l.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := host.Attrs().MasterIndex, br.Attrs().Index; got != want {
		t.Errorf("host veth master index = %d, want sandal0 (%d)", got, want)
	}

	// And with sandal0 already there.
	l2 := Link{Id: "exveth1", Type: LinkTypeVeth, Master: DefaultBridgeInterface}
	if err := l2.createVeth(); err != nil {
		t.Fatalf("createVeth with sandal0 present: %v", err)
	}
	host2, err := netlink.LinkByName("s-" + l2.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := host2.Attrs().MasterIndex, br.Attrs().Index; got != want {
		t.Errorf("second host veth master index = %d, want sandal0 (%d)", got, want)
	}
}

// Two first runs can both miss sandal0 and both add it. The one whose add
// fails with EEXIST uses the bridge the other one created.
func TestDefaultBridge_E2E_LostCreateRace(t *testing.T) {
	setupBridgeLab(t)
	prev := linkAdd
	linkAdd = func(link netlink.Link) error {
		// The other run adds sandal0 between our lookup and our add.
		winner := &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{
			Name: DefaultBridgeInterface, HardwareAddr: defaultBridgeMAC()}}
		if err := netlink.LinkAdd(winner); err != nil {
			t.Fatalf("winner add: %v", err)
		}
		return netlink.LinkAdd(link)
	}
	t.Cleanup(func() { linkAdd = prev })

	br, err := CreateDefaultBridge()
	if err != nil && err != os.ErrExist {
		t.Fatalf("CreateDefaultBridge after losing the add race: %v", err)
	}
	if br == nil || br.Attrs().Name != DefaultBridgeInterface {
		t.Fatalf("CreateDefaultBridge returned link %v, want sandal0", br)
	}
	if got, want := bridgeMAC(t), labBridgeMAC(); got != want {
		t.Errorf("mac = %s, want %s", got, want)
	}
}

// Concurrent first runs in one netns all get sandal0 without an error.
func TestDefaultBridge_E2E_ConcurrentCreate(t *testing.T) {
	setupBridgeLab(t)
	ns, err := netns.Get()
	if err != nil {
		t.Fatal(err)
	}
	defer ns.Close()

	const rounds, runs = 20, 8
	for r := 0; r < rounds; r++ {
		start := make(chan struct{})
		errs := make(chan error, runs)
		for i := 0; i < runs; i++ {
			go func() {
				// The thread stays in the lab netns and exits with the goroutine.
				runtime.LockOSThread()
				if err := netns.Set(ns); err != nil {
					errs <- err
					return
				}
				<-start
				br, err := CreateDefaultBridge()
				if err == nil || err == os.ErrExist {
					if br == nil {
						err = errors.New("no link returned")
					} else {
						err = nil
					}
				}
				errs <- err
			}()
		}
		close(start)
		for i := 0; i < runs; i++ {
			if err := <-errs; err != nil {
				t.Fatalf("round %d: CreateDefaultBridge: %v", r, err)
			}
		}
		br, err := netlink.LinkByName(DefaultBridgeInterface)
		if err != nil {
			t.Fatal(err)
		}
		if err := netlink.LinkDel(br); err != nil {
			t.Fatal(err)
		}
	}
}
