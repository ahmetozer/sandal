//go:build linux && (amd64 || arm64)

package kvm

import (
	"encoding/binary"
	"fmt"
	"os"
	"sync"
)

// VirtioBlkDevice implements a virtio-blk device backed by a raw disk image.
// Device ID: 2 (VIRTIO_ID_BLOCK)
// Queue 0: Request queue
//
// Each request has:
//   - Header (readable): type, reserved, sector
//   - Data (readable for write, writable for read)
//   - Status (writable): 1 byte result

const (
	virtioDevBlock = 2

	// Virtio block feature bits
	virtioBlkFSizeMax  = 1 << 1  // Maximum size of any single segment
	virtioBlkFSEGMax   = 1 << 2  // Maximum number of segments in a request
	virtioBlkFGeometry = 1 << 4  // Disk geometry available
	virtioBlkFRO       = 1 << 5  // Device is read-only
	virtioBlkFBLKSize  = 1 << 6  // Block size
	virtioBlkFFlush    = 1 << 9  // Flush command supported
	virtioBlkFTopology = 1 << 10 // Topology info available

	// Virtio block request types
	virtioBlkTIn    = 0 // Read
	virtioBlkTOut   = 1 // Write
	virtioBlkTFlush = 4 // Flush

	// Virtio block status
	virtioBlkSOK     = 0
	virtioBlkSIOErr  = 1
	virtioBlkSUnsup  = 2

	// Sector size
	sectorSize = 512
)

type VirtioBlkDevice struct {
	file     *os.File
	readOnly bool
	size     uint64 // device size in bytes

	mu     sync.Mutex
	stopCh chan struct{}
}

// NewVirtioBlkDevice creates a virtio-blk device backed by a disk image file.
func NewVirtioBlkDevice(path string, readOnly bool) (*VirtioBlkDevice, error) {
	flag := os.O_RDWR
	if readOnly {
		flag = os.O_RDONLY
	}
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return nil, fmt.Errorf("opening disk %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat disk %s: %w", path, err)
	}
	return &VirtioBlkDevice{
		file:     f,
		readOnly: readOnly,
		size:     uint64(info.Size()),
		stopCh:   make(chan struct{}),
	}, nil
}

func (d *VirtioBlkDevice) DeviceID() uint32 { return virtioDevBlock }
func (d *VirtioBlkDevice) Tag() string      { return "" }

func (d *VirtioBlkDevice) Features() uint64 {
	f := uint64(virtioBlkFFlush | virtioBlkFBLKSize | virtioBlkFSEGMax | virtioBlkFSizeMax)
	if d.readOnly {
		f |= virtioBlkFRO
	}
	return f
}

func (d *VirtioBlkDevice) ConfigRead(offset uint32, size uint32) uint32 {
	// struct virtio_blk_config {
	//   le64 capacity;       // offset 0: size in 512-byte sectors
	//   le32 size_max;       // offset 8: max segment size
	//   le32 seg_max;        // offset 12: max segments per request
	//   struct { le16 cylinders; u8 heads; u8 sectors; } geometry; // offset 16
	//   le32 blk_size;       // offset 20: block size
	//   ... (topology, etc.)
	// }
	config := make([]byte, 32)
	capacity := d.size / sectorSize
	binary.LittleEndian.PutUint64(config[0:], capacity)
	binary.LittleEndian.PutUint32(config[8:], 1<<20)  // size_max: 1MB
	binary.LittleEndian.PutUint32(config[12:], 128)    // seg_max
	binary.LittleEndian.PutUint32(config[20:], sectorSize) // blk_size

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

func (d *VirtioBlkDevice) ConfigWrite(offset uint32, size uint32, val uint32) {
	// Config is read-only for block devices
}

func (d *VirtioBlkDevice) HandleQueue(queueIdx int, dev *virtioMMIODev) {
	if queueIdx != 0 {
		return
	}
	dev.ProcessAvailRing(0, func(readBufs, writeBufs [][]byte) uint32 {
		return d.handleRequest(readBufs, writeBufs)
	})
}

// handleRequest processes a single virtio-blk request.
// Layout: [header(16 bytes, readable)] [data buffers] [status(1 byte, writable)]
func (d *VirtioBlkDevice) handleRequest(readBufs, writeBufs [][]byte) uint32 {
	if len(readBufs) == 0 || len(writeBufs) == 0 {
		return 0
	}

	// Parse header from first readable buffer (16 bytes)
	header := readBufs[0]
	if len(header) < 16 {
		return 0
	}
	reqType := binary.LittleEndian.Uint32(header[0:])
	sector := binary.LittleEndian.Uint64(header[8:])

	// Status byte is the last writable buffer
	statusBuf := writeBufs[len(writeBufs)-1]

	var written uint32
	status := uint8(virtioBlkSOK)

	switch reqType {
	case virtioBlkTIn:
		// Read: fill writable buffers (except last = status) with disk data
		offset := int64(sector) * sectorSize
		dataBufs := writeBufs[:len(writeBufs)-1]
		for _, buf := range dataBufs {
			n, err := d.file.ReadAt(buf, offset)
			if err != nil && n == 0 {
				status = virtioBlkSIOErr
				break
			}
			offset += int64(n)
			written += uint32(n)
		}

	case virtioBlkTOut:
		// Write: read data from readable buffers (skip header) and write to disk
		if d.readOnly {
			status = virtioBlkSIOErr
		} else {
			offset := int64(sector) * sectorSize
			dataBufs := readBufs[1:]
			for _, buf := range dataBufs {
				n, err := d.file.WriteAt(buf, offset)
				if err != nil {
					status = virtioBlkSIOErr
					break
				}
				offset += int64(n)
			}
		}

	case virtioBlkTFlush:
		if err := d.file.Sync(); err != nil {
			status = virtioBlkSIOErr
		}

	default:
		status = virtioBlkSUnsup
	}

	// Write status byte
	if len(statusBuf) > 0 {
		statusBuf[0] = status
		written++ // count the status byte
	}

	return written
}

func (d *VirtioBlkDevice) Close() error {
	return d.file.Close()
}
