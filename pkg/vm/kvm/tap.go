//go:build linux

package kvm

import (
	"fmt"
	"os"
	"unsafe"
)

const (
	tunDevice = "/dev/net/tun"

	// From linux/if_tun.h
	ifReqSize = 40 // sizeof(struct ifreq)

	iffTAP   = 0x0002
	iffNoPi  = 0x1000 // No packet info header
	iffVnetHdr = 0x4000 // Include virtio_net_hdr

	tunSetIFF   = 0x400454CA // TUNSETIFF
	tunSetOffload = 0x400454D0
	tunSetVnetHDRSz = 0x400454D8
)

// tapDevice represents an open TAP network device.
type tapDevice struct {
	file *os.File
	name string
}

// createTAP creates a TAP device and returns the open file descriptor.
// The TAP device is created with IFF_TAP | IFF_NO_PI | IFF_VNET_HDR.
func createTAP(name string) (*tapDevice, error) {
	f, err := os.OpenFile(tunDevice, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", tunDevice, err)
	}

	// Build ifreq struct
	var ifr [ifReqSize]byte
	copy(ifr[:], name)
	// Set flags at offset 16 (ifr_ifru union)
	flags := uint16(iffTAP | iffNoPi | iffVnetHdr)
	ifr[16] = byte(flags)
	ifr[17] = byte(flags >> 8)

	if _, err := ioctlPtr(int(f.Fd()), tunSetIFF, unsafe.Pointer(&ifr[0])); err != nil {
		f.Close()
		return nil, fmt.Errorf("TUNSETIFF: %w", err)
	}

	// Extract actual name (kernel may have modified it)
	actualName := ""
	for i, b := range ifr[:16] {
		if b == 0 {
			actualName = string(ifr[:i])
			break
		}
	}

	// Set vnet header size to match virtio_net_hdr_v1 (12 bytes)
	hdrSize := uint32(12)
	if _, err := ioctlPtr(int(f.Fd()), tunSetVnetHDRSz, unsafe.Pointer(&hdrSize)); err != nil {
		// Not fatal — some kernels don't support this
		_ = err
	}

	return &tapDevice{
		file: f,
		name: actualName,
	}, nil
}

func (t *tapDevice) Fd() int {
	return int(t.file.Fd())
}

func (t *tapDevice) Close() error {
	return t.file.Close()
}

