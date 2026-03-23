//go:build linux

package kvm

import (
	"encoding/binary"
	"sync"
	"unsafe"
)

// Virtio-MMIO transport implementation (virtio v2 / modern)
// See: https://docs.oasis-open.org/virtio/virtio/v1.1/virtio-v1.1.html

const (
	// Virtio MMIO register offsets
	virtioMMIOMagic         = 0x000 // "virt" (0x74726976)
	virtioMMIOVersion       = 0x004 // 2 for modern
	virtioMMIODeviceID      = 0x008
	virtioMMIOVendorID      = 0x00C
	virtioMMIODevFeatures   = 0x010
	virtioMMIODevFeatSel    = 0x014
	virtioMMIODrvFeatures   = 0x020
	virtioMMIODrvFeatSel    = 0x024
	virtioMMIOQueueSel      = 0x030
	virtioMMIOQueueNumMax   = 0x034
	virtioMMIOQueueNum      = 0x038
	virtioMMIOQueueReady    = 0x044
	virtioMMIOQueueNotify   = 0x050
	virtioMMIOInterruptStat = 0x060
	virtioMMIOInterruptAck  = 0x064
	virtioMMIOStatus        = 0x070
	virtioMMIOQueueDescLow  = 0x080
	virtioMMIOQueueDescHigh = 0x084
	virtioMMIOQueueDrvLow   = 0x090
	virtioMMIOQueueDrvHigh  = 0x094
	virtioMMIOQueueDevLow   = 0x0A0
	virtioMMIOQueueDevHigh  = 0x0A4
	virtioMMIOConfigGen     = 0x0FC
	virtioMMIOConfig        = 0x100

	virtioMMIORegionSize = 0x200

	// Virtio MMIO magic
	virtioMagicValue = 0x74726976

	// Virtio device status bits
	virtioStatusAck       = 1
	virtioStatusDriver    = 2
	virtioStatusFeatOK    = 8
	virtioStatusDriverOK  = 4

	// Virtio device IDs
	virtioDevFS  = 26
	virtioDevNet = 1

	// Virtio feature bits
	virtioFVersion1 = 32 // bit in feature word 1

	// Virtqueue sizes
	virtqueueMaxSize = 256

	// Descriptor flags
	vringDescFNext  = 1
	vringDescFWrite = 2

)

// virtqueue represents a single virtio virtqueue with split ring layout.
type virtqueue struct {
	num       uint32 // queue size (number of descriptors)
	ready     bool
	descAddr  uint64 // guest physical address of descriptor table
	drvAddr   uint64 // guest physical address of available ring (driver area)
	devAddr   uint64 // guest physical address of used ring (device area)
	lastAvail uint16 // last available index we processed
}

// vringDesc is a virtqueue descriptor (16 bytes each)
type vringDesc struct {
	Addr  uint64
	Len   uint32
	Flags uint16
	Next  uint16
}

// virtioDevice is the interface that specific virtio devices implement.
type virtioDevice interface {
	DeviceID() uint32
	Features() uint64
	ConfigRead(offset uint32, size uint32) uint32
	ConfigWrite(offset uint32, size uint32, val uint32)
	HandleQueue(queueIdx int, dev *virtioMMIODev)
	Tag() string // for virtiofs tag, empty for other devices
}

// virtioMMIODev represents a virtio device exposed via MMIO transport.
type virtioMMIODev struct {
	baseAddr uint64
	irqNum   uint32
	vmFd     int
	mem      []byte // guest memory

	device virtioDevice

	mu             sync.Mutex
	status         uint32
	devFeatSel     uint32
	drvFeatSel     uint32
	drvFeatures    uint64
	queueSel       uint32
	queues         [3]virtqueue // most devices need <= 3 queues
	interruptStat  uint32
	configGen      uint32
}

func newVirtioMMIODev(baseAddr uint64, irqNum uint32, vmFd int, mem []byte, dev virtioDevice) *virtioMMIODev {
	return &virtioMMIODev{
		baseAddr: baseAddr,
		irqNum:   irqNum,
		vmFd:     vmFd,
		mem:      mem,
		device:   dev,
	}
}

// HandleMMIO processes a virtio-MMIO register access.
// Returns true if the address was handled.
func (v *virtioMMIODev) HandleMMIO(addr uint64, length uint32, isWrite uint8, data []byte) bool {
	if addr < v.baseAddr || addr >= v.baseAddr+virtioMMIORegionSize {
		return false
	}
	offset := uint32(addr - v.baseAddr)

	v.mu.Lock()
	defer v.mu.Unlock()

	if isWrite != 0 {
		val := readLE(data, length)
		v.writeReg(offset, val)
	} else {
		val := v.readReg(offset, length)
		writeLE(data, length, val)
	}
	return true
}

func (v *virtioMMIODev) readReg(offset uint32, size uint32) uint32 {
	switch offset {
	case virtioMMIOMagic:
		return virtioMagicValue
	case virtioMMIOVersion:
		return 2
	case virtioMMIODeviceID:
		return v.device.DeviceID()
	case virtioMMIOVendorID:
		return 0x554D4551 // "QEMU" for compatibility
	case virtioMMIODevFeatures:
		features := v.device.Features()
		// Always advertise VIRTIO_F_VERSION_1
		features |= (1 << virtioFVersion1)
		if v.devFeatSel == 0 {
			return uint32(features)
		}
		return uint32(features >> 32)
	case virtioMMIOQueueNumMax:
		return virtqueueMaxSize
	case virtioMMIOQueueReady:
		q := v.currentQueue()
		if q == nil {
			return 0
		}
		if q.ready {
			return 1
		}
		return 0
	case virtioMMIOInterruptStat:
		return v.interruptStat
	case virtioMMIOStatus:
		return v.status
	case virtioMMIOConfigGen:
		return v.configGen
	default:
		if offset >= virtioMMIOConfig {
			return v.device.ConfigRead(offset-virtioMMIOConfig, size)
		}
		return 0
	}
}

func (v *virtioMMIODev) writeReg(offset uint32, val uint32) {
	switch offset {
	case virtioMMIODevFeatSel:
		v.devFeatSel = val
	case virtioMMIODrvFeatures:
		if v.drvFeatSel == 0 {
			v.drvFeatures = (v.drvFeatures & 0xFFFFFFFF00000000) | uint64(val)
		} else {
			v.drvFeatures = (v.drvFeatures & 0x00000000FFFFFFFF) | (uint64(val) << 32)
		}
	case virtioMMIODrvFeatSel:
		v.drvFeatSel = val
	case virtioMMIOQueueSel:
		v.queueSel = val
	case virtioMMIOQueueNum:
		if q := v.currentQueue(); q != nil {
			q.num = val
		}
	case virtioMMIOQueueReady:
		if q := v.currentQueue(); q != nil {
			q.ready = val != 0
		}
	case virtioMMIOQueueNotify:
		// Guest is notifying us that there are buffers available
		go v.device.HandleQueue(int(val), v)
	case virtioMMIOInterruptAck:
		v.interruptStat &^= val
	case virtioMMIOStatus:
		v.status = val
		if val == 0 {
			// Device reset
			v.reset()
		}
	case virtioMMIOQueueDescLow:
		if q := v.currentQueue(); q != nil {
			q.descAddr = (q.descAddr & 0xFFFFFFFF00000000) | uint64(val)
		}
	case virtioMMIOQueueDescHigh:
		if q := v.currentQueue(); q != nil {
			q.descAddr = (q.descAddr & 0x00000000FFFFFFFF) | (uint64(val) << 32)
		}
	case virtioMMIOQueueDrvLow:
		if q := v.currentQueue(); q != nil {
			q.drvAddr = (q.drvAddr & 0xFFFFFFFF00000000) | uint64(val)
		}
	case virtioMMIOQueueDrvHigh:
		if q := v.currentQueue(); q != nil {
			q.drvAddr = (q.drvAddr & 0x00000000FFFFFFFF) | (uint64(val) << 32)
		}
	case virtioMMIOQueueDevLow:
		if q := v.currentQueue(); q != nil {
			q.devAddr = (q.devAddr & 0xFFFFFFFF00000000) | uint64(val)
		}
	case virtioMMIOQueueDevHigh:
		if q := v.currentQueue(); q != nil {
			q.devAddr = (q.devAddr & 0x00000000FFFFFFFF) | (uint64(val) << 32)
		}
	default:
		if offset >= virtioMMIOConfig {
			v.device.ConfigWrite(offset-virtioMMIOConfig, 4, val)
		}
	}
}

func (v *virtioMMIODev) currentQueue() *virtqueue {
	if v.queueSel >= uint32(len(v.queues)) {
		return nil
	}
	return &v.queues[v.queueSel]
}

func (v *virtioMMIODev) reset() {
	v.status = 0
	v.devFeatSel = 0
	v.drvFeatSel = 0
	v.drvFeatures = 0
	v.queueSel = 0
	v.interruptStat = 0
	for i := range v.queues {
		v.queues[i] = virtqueue{}
	}
}

// injectIRQ sends an interrupt to the guest via KVM.
func (v *virtioMMIODev) injectIRQ() {
	v.interruptStat |= 1 // used buffer notification
	// Encode SPI for ARM64 KVM_IRQ_LINE
	spiIRQ := (uint32(kvmARMIRQTypeSPI) << kvmARMIRQTypeShift) | v.irqNum
	irq := kvmIRQLevel{
		IRQ:   spiIRQ,
		Level: 1,
	}
	ioctlPtr(v.vmFd, kvmIRQLine, unsafe.Pointer(&irq))
	// De-assert immediately (edge trigger behavior)
	irq.Level = 0
	ioctlPtr(v.vmFd, kvmIRQLine, unsafe.Pointer(&irq))
}

// ProcessAvailRing processes all available descriptors in a queue.
// For each descriptor chain, it calls the handler function with the
// readable and writable buffers. The handler returns the number of
// bytes written to writable buffers.
func (v *virtioMMIODev) ProcessAvailRing(queueIdx int, handler func(readBufs, writeBufs [][]byte) uint32) {
	if queueIdx >= len(v.queues) {
		return
	}
	q := &v.queues[queueIdx]
	if !q.ready || q.num == 0 {
		return
	}

	processed := false
	for {
		// Read the available index from guest memory
		availIdx := v.readGuestU16(q.drvAddr + 2)
		if q.lastAvail == availIdx {
			break
		}

		// Read the descriptor index from the available ring
		ringOff := 4 + uint64(q.lastAvail%uint16(q.num))*2
		descIdx := v.readGuestU16(q.drvAddr + ringOff)

		// Walk the descriptor chain
		var readBufs, writeBufs [][]byte
		idx := descIdx
		for {
			desc := v.readDescriptor(q, idx)
			gpa := desc.Addr
			buf := v.guestSlice(gpa, uint64(desc.Len))
			if buf != nil {
				if desc.Flags&vringDescFWrite != 0 {
					writeBufs = append(writeBufs, buf)
				} else {
					readBufs = append(readBufs, buf)
				}
			}
			if desc.Flags&vringDescFNext == 0 {
				break
			}
			idx = desc.Next
		}

		written := handler(readBufs, writeBufs)

		// Write to used ring
		usedIdx := v.readGuestU16(q.devAddr + 2)
		usedRingOff := 4 + uint64(usedIdx%uint16(q.num))*8
		v.writeGuestU32(q.devAddr+usedRingOff, uint32(descIdx))
		v.writeGuestU32(q.devAddr+usedRingOff+4, written)
		v.writeGuestU16(q.devAddr+2, usedIdx+1)

		q.lastAvail++
		processed = true
	}

	if processed {
		v.injectIRQ()
	}
}

func (v *virtioMMIODev) readDescriptor(q *virtqueue, idx uint16) vringDesc {
	off := q.descAddr + uint64(idx)*16
	return vringDesc{
		Addr:  v.readGuestU64(off),
		Len:   v.readGuestU32(off + 8),
		Flags: v.readGuestU16(off + 12),
		Next:  v.readGuestU16(off + 14),
	}
}

// Guest memory access helpers - translate GPA to HVA
func (v *virtioMMIODev) guestSlice(gpa uint64, size uint64) []byte {
	offset := gpa - guestMemBase
	if offset+size > uint64(len(v.mem)) {
		return nil
	}
	return v.mem[offset : offset+size]
}

func (v *virtioMMIODev) readGuestU16(gpa uint64) uint16 {
	b := v.guestSlice(gpa, 2)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint16(b)
}

func (v *virtioMMIODev) readGuestU32(gpa uint64) uint32 {
	b := v.guestSlice(gpa, 4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}

func (v *virtioMMIODev) readGuestU64(gpa uint64) uint64 {
	b := v.guestSlice(gpa, 8)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint64(b)
}

func (v *virtioMMIODev) writeGuestU16(gpa uint64, val uint16) {
	b := v.guestSlice(gpa, 2)
	if b != nil {
		binary.LittleEndian.PutUint16(b, val)
	}
}

func (v *virtioMMIODev) writeGuestU32(gpa uint64, val uint32) {
	b := v.guestSlice(gpa, 4)
	if b != nil {
		binary.LittleEndian.PutUint32(b, val)
	}
}

// Helper to read little-endian value from MMIO data buffer
func readLE(data []byte, size uint32) uint32 {
	switch size {
	case 1:
		return uint32(data[0])
	case 2:
		return uint32(binary.LittleEndian.Uint16(data[:2]))
	case 4:
		return binary.LittleEndian.Uint32(data[:4])
	}
	return 0
}

// Helper to write little-endian value to MMIO data buffer
func writeLE(data []byte, size uint32, val uint32) {
	switch size {
	case 1:
		data[0] = byte(val)
	case 2:
		binary.LittleEndian.PutUint16(data[:2], uint16(val))
	case 4:
		binary.LittleEndian.PutUint32(data[:4], val)
	}
}

