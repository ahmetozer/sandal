//go:build linux

package kvm

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"unsafe"

	"golang.org/x/sys/unix"
)

// vhost ioctl constants for vsock device setup.
const (
	vhostSetOwner       = 0x0000AF01
	vhostGetFeatures    = 0x8008AF00
	vhostSetFeatures    = 0x4008AF00
	vhostSetMemTable    = 0x4008AF03
	vhostSetVringNum    = 0x4008AF10
	vhostSetVringAddr   = 0x4028AF11
	vhostSetVringBase   = 0x4008AF12
	vhostSetVringKick   = 0x4008AF20
	vhostSetVringCall   = 0x4008AF21
	vhostVsockSetCID    = 0x4008AF60
	vhostVsockSetRunning = 0x4004AF61
)

// VirtioVsockDevice implements virtio-vsock using the vhost-vsock kernel backend.
type VirtioVsockDevice struct {
	guestCID uint64
	vhostFd  int
	kickFds  [2]int
	callFds  [2]int
}

// NewVirtioVsockDevice opens /dev/vhost-vsock and allocates a unique guest
// CID by retrying VHOST_VSOCK_SET_GUEST_CID from 3 upward until one succeeds
// (vhost-vsock rejects duplicates with EADDRINUSE). The opened fd is retained
// on the device and reused by SetupVhost, so SetupVhost never has to
// re-negotiate the CID. This is what allows multiple concurrent VMs to have
// non-colliding vsock routes.
func NewVirtioVsockDevice() (*VirtioVsockDevice, error) {
	fd, err := unix.Open("/dev/vhost-vsock", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		fd, err = unix.Open("/devtmpfs/vhost-vsock", unix.O_RDWR|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, fmt.Errorf("open vhost-vsock: %w", err)
		}
	}

	// Set owner once so the CID ioctl is accepted.
	if _, err := ioctlPtr(fd, vhostSetOwner, nil); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("VHOST_SET_OWNER: %w", err)
	}

	// CIDs 0-2 are reserved (hypervisor/local/host). Probe upward until the
	// kernel accepts one. 128 is a generous ceiling for concurrent VMs.
	var chosen uint64
	for candidate := uint64(3); candidate < 3+128; candidate++ {
		cid := candidate
		if _, err := ioctlPtr(fd, vhostVsockSetCID, unsafe.Pointer(&cid)); err == nil {
			chosen = candidate
			break
		}
	}
	if chosen == 0 {
		unix.Close(fd)
		return nil, fmt.Errorf("no free vhost-vsock CID in [3, 131)")
	}

	return &VirtioVsockDevice{
		guestCID: chosen,
		vhostFd:  fd,
		kickFds:  [2]int{-1, -1},
		callFds:  [2]int{-1, -1},
	}, nil
}

// GuestCID returns the vsock CID assigned to this VM. Used by the host
// management socket relay to dial the correct guest.
func (d *VirtioVsockDevice) GuestCID() uint64 { return d.guestCID }

func (d *VirtioVsockDevice) DeviceID() uint32 { return virtioDevVsock }
func (d *VirtioVsockDevice) Tag() string       { return "" }
func (d *VirtioVsockDevice) Features() uint64  { return 0 }

// ConfigRead returns the guest CID as a le64 (8-byte config space).
func (d *VirtioVsockDevice) ConfigRead(offset uint32, size uint32) uint32 {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], d.guestCID)
	if offset+size > 8 {
		return 0
	}
	switch size {
	case 1:
		return uint32(buf[offset])
	case 2:
		return uint32(binary.LittleEndian.Uint16(buf[offset : offset+2]))
	case 4:
		return binary.LittleEndian.Uint32(buf[offset : offset+4])
	}
	return 0
}

func (d *VirtioVsockDevice) ConfigWrite(offset uint32, size uint32, val uint32) {}

// HandleQueue relays guest notifications to the vhost kernel via the kick eventfd.
func (d *VirtioVsockDevice) HandleQueue(queueIdx int, dev *virtioMMIODev) {
	if queueIdx < 0 || queueIdx >= len(d.kickFds) || d.kickFds[queueIdx] < 0 {
		return
	}
	buf := [8]byte{1, 0, 0, 0, 0, 0, 0, 0}
	unix.Write(d.kickFds[queueIdx], buf[:])
}

// SetupVhost configures the vhost backend's memory table and vrings on the
// fd that was already opened (and bound to a unique CID with VHOST_SET_OWNER
// + VHOST_VSOCK_SET_GUEST_CID) by NewVirtioVsockDevice.
func (d *VirtioVsockDevice) SetupVhost(dev *virtioMMIODev, mem []byte, memBase uint64, memSize uint64) error {
	if d.vhostFd < 0 {
		return fmt.Errorf("vhost-vsock fd not allocated")
	}
	fd := d.vhostFd

	// Get and set features.
	var features uint64
	if _, err := ioctlPtr(fd, vhostGetFeatures, unsafe.Pointer(&features)); err != nil {
		return fmt.Errorf("VHOST_GET_FEATURES: %w", err)
	}
	features &= dev.drvFeatures
	if _, err := ioctlPtr(fd, vhostSetFeatures, unsafe.Pointer(&features)); err != nil {
		return fmt.Errorf("VHOST_SET_FEATURES: %w", err)
	}

	// Set memory table: single region mapping guest RAM.
	// struct vhost_memory { nregions u32, padding u32, regions[] }
	// struct vhost_memory_region { guest_phys_addr u64, memory_size u64, userspace_addr u64, flags_padding u64 }
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
		return fmt.Errorf("VHOST_SET_MEM_TABLE: %w", err)
	}

	// Configure vrings 0 (rx) and 1 (tx).
	for i := 0; i < 2; i++ {
		// Create eventfds for kick and call.
		kickFd, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
		if err != nil {
			return fmt.Errorf("eventfd kick[%d]: %w", i, err)
		}
		d.kickFds[i] = kickFd

		callFd, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
		if err != nil {
			return fmt.Errorf("eventfd call[%d]: %w", i, err)
		}
		d.callFds[i] = callFd

		q := &dev.queues[i]

		// Set vring num.
		vringState := [2]uint32{uint32(i), q.num}
		if _, err := ioctlPtr(fd, vhostSetVringNum, unsafe.Pointer(&vringState)); err != nil {
			return fmt.Errorf("VHOST_SET_VRING_NUM[%d]: %w", i, err)
		}

		// Set vring base.
		vringBase := [2]uint32{uint32(i), 0}
		if _, err := ioctlPtr(fd, vhostSetVringBase, unsafe.Pointer(&vringBase)); err != nil {
			return fmt.Errorf("VHOST_SET_VRING_BASE[%d]: %w", i, err)
		}

		// GPA to HVA translation for vring addresses.
		descHVA := uintptr(unsafe.Pointer(&mem[q.descAddr-memBase]))
		drvHVA := uintptr(unsafe.Pointer(&mem[q.drvAddr-memBase]))
		devHVA := uintptr(unsafe.Pointer(&mem[q.devAddr-memBase]))

		// struct vhost_vring_addr: index u32, flags u32, desc u64, used u64, avail u64, log u64
		type vhostVringAddr struct {
			Index    uint32
			Flags    uint32
			DescAddr uint64
			UsedAddr uint64
			AvailAddr uint64
			LogAddr  uint64
		}
		vringAddr := vhostVringAddr{
			Index:     uint32(i),
			DescAddr:  uint64(descHVA),
			UsedAddr:  uint64(devHVA),
			AvailAddr: uint64(drvHVA),
		}
		if _, err := ioctlPtr(fd, vhostSetVringAddr, unsafe.Pointer(&vringAddr)); err != nil {
			return fmt.Errorf("VHOST_SET_VRING_ADDR[%d]: %w", i, err)
		}

		// Set vring kick fd.
		vringFile := [2]uint32{uint32(i), uint32(kickFd)}
		if _, err := ioctlPtr(fd, vhostSetVringKick, unsafe.Pointer(&vringFile)); err != nil {
			return fmt.Errorf("VHOST_SET_VRING_KICK[%d]: %w", i, err)
		}

		// Set vring call fd.
		vringCallFile := [2]uint32{uint32(i), uint32(callFd)}
		if _, err := ioctlPtr(fd, vhostSetVringCall, unsafe.Pointer(&vringCallFile)); err != nil {
			return fmt.Errorf("VHOST_SET_VRING_CALL[%d]: %w", i, err)
		}

		// Start goroutine to relay call eventfd to IRQ injection.
		go func(cfd int) {
			buf := make([]byte, 8)
			for {
				_, err := unix.Read(cfd, buf)
				if err != nil {
					if err == unix.EAGAIN {
						// Use poll to wait for data.
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

	// Start vhost.
	running := uint32(1)
	if _, err := ioctlPtr(fd, vhostVsockSetRunning, unsafe.Pointer(&running)); err != nil {
		return fmt.Errorf("VHOST_VSOCK_SET_RUNNING: %w", err)
	}

	slog.Info("vhost-vsock initialized", slog.Uint64("cid", d.guestCID))
	return nil
}

// Close stops the vhost backend and closes all file descriptors.
func (d *VirtioVsockDevice) Close() {
	if d.vhostFd >= 0 {
		// Stop vhost.
		running := uint32(0)
		ioctlPtr(d.vhostFd, vhostVsockSetRunning, unsafe.Pointer(&running))
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
}

// Ensure VirtioVsockDevice implements virtioDevice.
var _ virtioDevice = (*VirtioVsockDevice)(nil)
