//go:build linux && (amd64 || arm64)

package kvm

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// VirtioNetDevice implements a virtio-net device backed by a TAP interface.
// Queue 0 = RX (device -> driver), Queue 1 = TX (driver -> device)
//
// When /dev/vhost-net is available, the kernel handles all packet I/O
// directly (TAP ↔ virtqueue), eliminating userspace involvement per packet.
// Otherwise falls back to userspace TX/RX loops.
const (
	virtioNetFMAC    = 1 << 5  // Device has given MAC address
	virtioNetFStatus = 1 << 16 // Device status field supported

	// vhost-net ioctl
	vhostNetSetBackend = 0x4008AF30 // VHOST_NET_SET_BACKEND
)

type VirtioNetDevice struct {
	tap      *tapDevice
	mac      [6]byte
	stopCh   chan struct{}
	txNotify chan struct{} // signals persistent TX worker (userspace path)
	txBuf    []byte        // reusable buffer for multi-descriptor TX (userspace path)

	// vhost-net kernel backend (zero when not active)
	vhostFd  int
	kickFds  [2]int // per-queue eventfds: guest notify → kernel
	callFds  [2]int // per-queue eventfds: kernel → guest IRQ
	useVhost bool
}

// NewVirtioNetDevice creates a virtio-net device backed by the given TAP.
func NewVirtioNetDevice(tap *tapDevice, mac [6]byte) *VirtioNetDevice {
	return &VirtioNetDevice{
		tap:      tap,
		mac:      mac,
		stopCh:   make(chan struct{}),
		txNotify: make(chan struct{}, 1),
		txBuf:    make([]byte, 0, 65536),
		vhostFd:  -1,
		kickFds:  [2]int{-1, -1},
		callFds:  [2]int{-1, -1},
	}
}

func (d *VirtioNetDevice) DeviceID() uint32 { return virtioDevNet }
func (d *VirtioNetDevice) Tag() string      { return "" }

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
	if d.useVhost {
		// vhost-net: relay guest kick to kernel via eventfd
		if queueIdx < 0 || queueIdx >= len(d.kickFds) || d.kickFds[queueIdx] < 0 {
			return
		}
		buf := [8]byte{1, 0, 0, 0, 0, 0, 0, 0}
		unix.Write(d.kickFds[queueIdx], buf[:])
		return
	}

	// Userspace fallback
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

// SetupVhost opens /dev/vhost-net and configures the kernel backend.
// Called when the guest driver signals DRIVER_OK. On failure, the
// caller falls back to userspace I/O loops.
func (d *VirtioNetDevice) SetupVhost(dev *virtioMMIODev, mem []byte, memBase uint64, memSize uint64) error {
	fd, err := unix.Open("/dev/vhost-net", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		fd, err = unix.Open("/devtmpfs/vhost-net", unix.O_RDWR|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("open vhost-net: %w", err)
		}
	}
	d.vhostFd = fd

	// Set owner.
	if _, err := ioctlPtr(fd, vhostSetOwner, nil); err != nil {
		d.closeVhost()
		return fmt.Errorf("VHOST_SET_OWNER: %w", err)
	}

	// Get and set features.
	var features uint64
	if _, err := ioctlPtr(fd, vhostGetFeatures, unsafe.Pointer(&features)); err != nil {
		d.closeVhost()
		return fmt.Errorf("VHOST_GET_FEATURES: %w", err)
	}
	features &= dev.drvFeatures
	if _, err := ioctlPtr(fd, vhostSetFeatures, unsafe.Pointer(&features)); err != nil {
		d.closeVhost()
		return fmt.Errorf("VHOST_SET_FEATURES: %w", err)
	}

	// Set memory table: single region mapping guest RAM.
	type vhostMemoryRegion struct {
		GuestPhysAddr uint64
		MemorySize    uint64
		UserspaceAddr uint64
		FlagsPadding  uint64
	}
	type vhostMemory struct {
		NRegions uint32
		Padding  uint32
		Regions  [1]vhostMemoryRegion
	}
	memTable := vhostMemory{
		NRegions: 1,
		Regions: [1]vhostMemoryRegion{{
			GuestPhysAddr: memBase,
			MemorySize:    memSize,
			UserspaceAddr: uint64(uintptr(unsafe.Pointer(&mem[0]))),
		}},
	}
	if _, err := ioctlPtr(fd, vhostSetMemTable, unsafe.Pointer(&memTable)); err != nil {
		d.closeVhost()
		return fmt.Errorf("VHOST_SET_MEM_TABLE: %w", err)
	}

	tapFd := d.tap.Fd()

	// Configure vrings 0 (RX) and 1 (TX).
	for i := 0; i < 2; i++ {
		kickFd, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
		if err != nil {
			d.closeVhost()
			return fmt.Errorf("eventfd kick[%d]: %w", i, err)
		}
		d.kickFds[i] = kickFd

		callFd, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
		if err != nil {
			d.closeVhost()
			return fmt.Errorf("eventfd call[%d]: %w", i, err)
		}
		d.callFds[i] = callFd

		q := &dev.queues[i]

		// Set vring num.
		vringState := [2]uint32{uint32(i), q.num}
		if _, err := ioctlPtr(fd, vhostSetVringNum, unsafe.Pointer(&vringState)); err != nil {
			d.closeVhost()
			return fmt.Errorf("VHOST_SET_VRING_NUM[%d]: %w", i, err)
		}

		// Set vring base.
		vringBase := [2]uint32{uint32(i), 0}
		if _, err := ioctlPtr(fd, vhostSetVringBase, unsafe.Pointer(&vringBase)); err != nil {
			d.closeVhost()
			return fmt.Errorf("VHOST_SET_VRING_BASE[%d]: %w", i, err)
		}

		// GPA to HVA translation for vring addresses.
		descHVA := uintptr(unsafe.Pointer(&mem[q.descAddr-memBase]))
		drvHVA := uintptr(unsafe.Pointer(&mem[q.drvAddr-memBase]))
		devHVA := uintptr(unsafe.Pointer(&mem[q.devAddr-memBase]))

		type vhostVringAddr struct {
			Index     uint32
			Flags     uint32
			DescAddr  uint64
			UsedAddr  uint64
			AvailAddr uint64
			LogAddr   uint64
		}
		vringAddr := vhostVringAddr{
			Index:     uint32(i),
			DescAddr:  uint64(descHVA),
			UsedAddr:  uint64(devHVA),
			AvailAddr: uint64(drvHVA),
		}
		if _, err := ioctlPtr(fd, vhostSetVringAddr, unsafe.Pointer(&vringAddr)); err != nil {
			d.closeVhost()
			return fmt.Errorf("VHOST_SET_VRING_ADDR[%d]: %w", i, err)
		}

		// Set kick fd (guest → kernel notification).
		vringFile := [2]uint32{uint32(i), uint32(kickFd)}
		if _, err := ioctlPtr(fd, vhostSetVringKick, unsafe.Pointer(&vringFile)); err != nil {
			d.closeVhost()
			return fmt.Errorf("VHOST_SET_VRING_KICK[%d]: %w", i, err)
		}

		// Set call fd (kernel → guest IRQ injection).
		vringCallFile := [2]uint32{uint32(i), uint32(callFd)}
		if _, err := ioctlPtr(fd, vhostSetVringCall, unsafe.Pointer(&vringCallFile)); err != nil {
			d.closeVhost()
			return fmt.Errorf("VHOST_SET_VRING_CALL[%d]: %w", i, err)
		}

		// Set backend: bind this queue to the TAP fd.
		backendFile := [2]uint32{uint32(i), uint32(tapFd)}
		if _, err := ioctlPtr(fd, vhostNetSetBackend, unsafe.Pointer(&backendFile)); err != nil {
			d.closeVhost()
			return fmt.Errorf("VHOST_NET_SET_BACKEND[%d]: %w", i, err)
		}

		// Relay call eventfd → guest IRQ injection.
		go func(cfd int) {
			buf := make([]byte, 8)
			for {
				_, err := unix.Read(cfd, buf)
				if err != nil {
					if err == unix.EAGAIN {
						fds := []unix.PollFd{{Fd: int32(cfd), Events: unix.POLLIN}}
						unix.Poll(fds, -1)
						continue
					}
					return // fd closed or error
				}
				dev.injectIRQ()
			}
		}(callFd)
	}

	d.useVhost = true
	slog.Debug("vhost-net initialized", slog.String("tap", d.tap.name))
	return nil
}

// StartIO sets up vhost-net if available, otherwise launches userspace
// TX/RX worker goroutines as fallback.
func (d *VirtioNetDevice) StartIO(dev *virtioMMIODev) {
	if d.useVhost {
		// Kernel handles all I/O — nothing to launch.
		return
	}
	go d.txLoop(dev)
	go d.rxLoop(dev)
}

// ---------- userspace fallback ----------

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

// ---------- cleanup ----------

func (d *VirtioNetDevice) closeVhost() {
	if d.vhostFd >= 0 {
		unix.Close(d.vhostFd)
		d.vhostFd = -1
	}
	for i := range d.kickFds {
		if d.kickFds[i] >= 0 {
			unix.Close(d.kickFds[i])
			d.kickFds[i] = -1
		}
	}
	for i := range d.callFds {
		if d.callFds[i] >= 0 {
			unix.Close(d.callFds[i])
			d.callFds[i] = -1
		}
	}
	d.useVhost = false
}

func (d *VirtioNetDevice) Stop() {
	close(d.stopCh)
	d.closeVhost()
}
