//go:build linux

package kvm

import (
	"encoding/binary"
	"log/slog"
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
	tap      *tapDevice
	mac      [6]byte
	stopCh   chan struct{}
	txNotify chan struct{} // signals persistent TX worker
	txBuf    []byte        // reusable buffer for multi-descriptor TX concatenation
}

// NewVirtioNetDevice creates a virtio-net device backed by the given TAP.
func NewVirtioNetDevice(tap *tapDevice, mac [6]byte) *VirtioNetDevice {
	return &VirtioNetDevice{
		tap:      tap,
		mac:      mac,
		stopCh:   make(chan struct{}),
		txNotify: make(chan struct{}, 1),
		txBuf:    make([]byte, 0, 65536),
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
		// RX queue — handled by rxLoop
	case 1:
		// Signal persistent TX worker (non-blocking; coalesces rapid kicks)
		select {
		case d.txNotify <- struct{}{}:
		default:
		}
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
	fd := d.tap.Fd()
	if len(readBufs) == 1 {
		n, err := unix.Write(fd, readBufs[0])
		if err != nil {
			slog.Error("virtio-net TAP write error", slog.Any("err", err))
			return 0
		}
		return uint32(n)
	}

	// Concatenate into reusable buffer — zero allocation in steady state.
	// Safe: txLoop is single-goroutine, so txBuf needs no synchronization.
	d.txBuf = d.txBuf[:0]
	for _, buf := range readBufs {
		d.txBuf = append(d.txBuf, buf...)
	}
	n, err := unix.Write(fd, d.txBuf)
	if err != nil {
		slog.Error("virtio-net TAP write error", slog.Any("err", err))
		return 0
	}
	return uint32(n)
}

// StartIO launches persistent TX and RX worker goroutines.
func (d *VirtioNetDevice) StartIO(dev *virtioMMIODev) {
	go d.txLoop(dev)
	go d.rxLoop(dev)
}

// txLoop is a persistent goroutine that processes TX descriptors when
// signalled by HandleQueue. Replaces per-kick goroutine spawning;
// the cap-1 channel naturally coalesces rapid kicks.
func (d *VirtioNetDevice) txLoop(dev *virtioMMIODev) {
	for {
		select {
		case <-d.stopCh:
			return
		case <-d.txNotify:
			dev.ProcessAvailRing(1, func(readBufs, writeBufs [][]byte) uint32 {
				return d.handleTX(readBufs)
			})
		}
	}
}

func (d *VirtioNetDevice) rxLoop(dev *virtioMMIODev) {
	fd := d.tap.Fd()
	buf := make([]byte, 65536)

	// rxHandler copies pkt (set before each call) into guest write buffers.
	// Defined once to avoid closure allocation per packet.
	var pkt []byte
	rxHandler := func(readBufs, writeBufs [][]byte) uint32 {
		if len(writeBufs) == 0 {
			slog.Warn("virtio-net RX: no write buffers available")
			return 0
		}
		written := 0
		remaining := pkt
		for _, wb := range writeBufs {
			n := copy(wb, remaining)
			written += n
			remaining = remaining[n:]
			if len(remaining) == 0 {
				break
			}
		}
		return uint32(written)
	}

	for {
		select {
		case <-d.stopCh:
			return
		default:
		}

		// Blocking read — first packet in a potential batch.
		n, err := unix.Read(fd, buf)
		if err != nil {
			select {
			case <-d.stopCh:
				return
			default:
				time.Sleep(10 * time.Millisecond)
				continue
			}
		}
		if n == 0 {
			continue
		}

		// Zero-copy: use buf slice directly as source for guest memory copy.
		// Safe because ProcessSingleAvailNoIRQ copies into guest memory
		// synchronously before returning, so buf won't be overwritten.
		pkt = buf[:n]
		if !dev.ProcessSingleAvailNoIRQ(0, rxHandler) {
			continue
		}

		// Try to batch more packets: poll with zero timeout to check if
		// more data is available, avoiding blocking on the TAP fd.
		// This amortizes one IRQ across up to rxBatchMax packets.
		const rxBatchMax = 16
		pollFds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		for i := 1; i < rxBatchMax; i++ {
			ready, _ := unix.Poll(pollFds, 0)
			if ready <= 0 {
				break
			}
			nn, err := unix.Read(fd, buf)
			if nn <= 0 || err != nil {
				break
			}
			pkt = buf[:nn]
			if !dev.ProcessSingleAvailNoIRQ(0, rxHandler) {
				break
			}
		}

		dev.injectIRQ()
	}
}

func (d *VirtioNetDevice) Stop() {
	close(d.stopCh)
}
