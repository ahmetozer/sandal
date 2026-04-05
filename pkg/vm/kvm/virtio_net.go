//go:build linux

package kvm

import (
	"encoding/binary"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// VirtioNetDevice implements a virtio-net device backed by a TAP interface.
// Queue 0 = RX (device -> driver), Queue 1 = TX (driver -> device)
const (
	virtioNetFMAC    = 1 << 5  // Device has given MAC address
	virtioNetFStatus = 1 << 16 // Device status field supported
)

type VirtioNetDevice struct {
	tap    *tapDevice
	mac    [6]byte
	mu     sync.Mutex
	stopCh chan struct{}
}

// NewVirtioNetDevice creates a virtio-net device backed by the given TAP.
func NewVirtioNetDevice(tap *tapDevice, mac [6]byte) *VirtioNetDevice {
	return &VirtioNetDevice{
		tap:    tap,
		mac:    mac,
		stopCh: make(chan struct{}),
	}
}

func (d *VirtioNetDevice) DeviceID() uint32 { return virtioDevNet }
func (d *VirtioNetDevice) Tag() string       { return "" }

func (d *VirtioNetDevice) Features() uint64 {
	return virtioNetFMAC | virtioNetFStatus
}

func (d *VirtioNetDevice) ConfigRead(offset uint32, size uint32) uint32 {
	// virtio_net_config: mac[6], status[2], max_virtqueue_pairs[2], mtu[2], ...
	config := make([]byte, 12)
	copy(config[0:6], d.mac[:])
	config[6] = 1 // status: VIRTIO_NET_S_LINK_UP

	if offset+size > uint32(len(config)) {
		return 0
	}
	switch size {
	case 1:
		return uint32(config[offset])
	case 2:
		return uint32(binary.LittleEndian.Uint16(config[offset:]))
	case 4:
		return binary.LittleEndian.Uint32(config[offset:])
	}
	return 0
}

func (d *VirtioNetDevice) ConfigWrite(offset uint32, size uint32, val uint32) {
	// Config is read-only
}

func (d *VirtioNetDevice) HandleQueue(queueIdx int, dev *virtioMMIODev) {
	switch queueIdx {
	case 0:
		// RX queue — nothing to do here; RX is handled by the background reader
	case 1:
		// TX queue — guest is sending packets
		dev.ProcessAvailRing(queueIdx, func(readBufs, writeBufs [][]byte) uint32 {
			return d.handleTX(readBufs)
		})
	}
}

func (d *VirtioNetDevice) handleTX(readBufs [][]byte) uint32 {
	if len(readBufs) == 0 {
		return 0
	}

	// With IFF_VNET_HDR on the TAP, each write must contain the complete
	// virtio_net_hdr followed by the Ethernet frame as a single write.
	// The guest typically splits these across separate descriptors (header
	// in one, packet data in the next), so we must concatenate them.
	// Use unix.Write to bypass Go's poller (TAP fds are not pollable).
	fd := d.tap.Fd()
	if len(readBufs) == 1 {
		n, err := unix.Write(fd, readBufs[0])
		if err != nil {
			slog.Error("virtio-net TAP write error", slog.Any("err", err))
			return 0
		}
		return uint32(n)
	}

	var total int
	for _, buf := range readBufs {
		total += len(buf)
	}
	pkt := make([]byte, 0, total)
	for _, buf := range readBufs {
		pkt = append(pkt, buf...)
	}
	n, err := unix.Write(fd, pkt)
	if err != nil {
		slog.Error("virtio-net TAP write error", slog.Any("err", err))
		return 0
	}
	return uint32(n)
}

// StartRX begins reading packets from the TAP device and injecting them
// into the guest's RX virtqueue.
func (d *VirtioNetDevice) StartRX(dev *virtioMMIODev) {
	go d.rxLoop(dev)
}

func (d *VirtioNetDevice) rxLoop(dev *virtioMMIODev) {
	fd := d.tap.Fd()
	buf := make([]byte, 65536)
	for {
		select {
		case <-d.stopCh:
			return
		default:
		}

		n, err := unix.Read(fd, buf)
		if err != nil {
			select {
			case <-d.stopCh:
				return
			default:
				// Avoid tight spin on persistent read errors.
				time.Sleep(10 * time.Millisecond)
				continue
			}
		}
		if n == 0 {
			continue
		}

		packet := make([]byte, n)
		copy(packet, buf[:n])

		// Inject packet into RX queue (queue 0)
		dev.ProcessAvailRing(0, func(readBufs, writeBufs [][]byte) uint32 {
			if len(writeBufs) == 0 {
				slog.Warn("virtio-net RX: no write buffers available")
				return 0
			}

			// Write virtio_net_hdr + packet data into write buffers
			written := 0
			remaining := packet
			for _, wb := range writeBufs {
				n := copy(wb, remaining)
				written += n
				remaining = remaining[n:]
				if len(remaining) == 0 {
					break
				}
			}
			return uint32(written)
		})
	}
}

func (d *VirtioNetDevice) Stop() {
	close(d.stopCh)
}
