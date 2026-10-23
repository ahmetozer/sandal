//go:build linux

package net

import (
	"bytes"
	"net"
	"testing"
)

func TestStableBridgeMAC_LocallyAdministeredUnicast(t *testing.T) {
	uplink := net.HardwareAddr{0x52, 0x54, 0x00, 0x12, 0x34, 0x56}
	for _, tc := range []struct {
		name      string
		machineID []byte
		uplink    net.HardwareAddr
	}{
		{"machine-id and uplink", []byte("0123456789abcdef0123456789abcdef"), uplink},
		{"machine-id only", []byte("0123456789abcdef0123456789abcdef"), nil},
		{"uplink only", nil, uplink},
	} {
		mac := stableBridgeMAC(tc.machineID, tc.uplink)
		if len(mac) != 6 {
			t.Fatalf("%s: mac = %v, want 6 bytes", tc.name, mac)
		}
		if mac[0]&0x01 != 0 {
			t.Errorf("%s: %v is multicast", tc.name, mac)
		}
		if mac[0]&0x02 == 0 {
			t.Errorf("%s: %v is not locally administered", tc.name, mac)
		}
	}
}

func TestStableBridgeMAC_Deterministic(t *testing.T) {
	id := []byte("0123456789abcdef0123456789abcdef")
	up := net.HardwareAddr{0x52, 0x54, 0x00, 0x12, 0x34, 0x56}
	a, b := stableBridgeMAC(id, up), stableBridgeMAC(id, up)
	if !bytes.Equal(a, b) {
		t.Fatalf("same seed gave %v and %v", a, b)
	}
}

// Hosts cloned from one image share a machine-id; their uplink MACs still
// differ, so a sandal0 bridged onto a shared L2 does not collide.
func TestStableBridgeMAC_SeedsDiffer(t *testing.T) {
	id := []byte("0123456789abcdef0123456789abcdef")
	up1 := net.HardwareAddr{0x52, 0x54, 0x00, 0x12, 0x34, 0x56}
	up2 := net.HardwareAddr{0x52, 0x54, 0x00, 0x12, 0x34, 0x57}
	if bytes.Equal(stableBridgeMAC(id, up1), stableBridgeMAC(id, up2)) {
		t.Error("different uplinks, same MAC")
	}
	if bytes.Equal(stableBridgeMAC(id, up1), stableBridgeMAC([]byte("fedcba9876543210fedcba9876543210"), up1)) {
		t.Error("different machine-ids, same MAC")
	}
}

func TestStableBridgeMAC_NoSeed(t *testing.T) {
	if mac := stableBridgeMAC(nil, nil); mac != nil {
		t.Fatalf("mac = %v, want nil without a seed", mac)
	}
}

func TestNeedsFixedMAC(t *testing.T) {
	for in, want := range map[string]bool{
		"0\n": false, // NET_ADDR_PERM
		"1\n": true,  // NET_ADDR_RANDOM: follows the lowest port MAC
		"2\n": true,  // NET_ADDR_STOLEN
		"3\n": false, // NET_ADDR_SET: user, udev or sandal chose it
		"":    false,
		"x":   false,
	} {
		if got := needsFixedMAC(in); got != want {
			t.Errorf("needsFixedMAC(%q) = %v, want %v", in, got, want)
		}
	}
}
