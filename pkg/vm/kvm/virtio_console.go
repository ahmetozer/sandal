//go:build linux

package kvm

import (
	"encoding/binary"
	"os"
	"sync"
	"time"
)

// VirtioConsoleDevice implements a virtio-console device.
// This provides /dev/hvc0 in the guest, which the kernel uses as
// the initial console.
//
// Device ID: 3 (VIRTIO_ID_CONSOLE)
// Queue 0: RX (device writes, driver reads — guest input)
// Queue 1: TX (driver writes, device reads — guest output)

const (
	virtioDevConsole = 3

	// Feature bits
	virtioConsoleFSize      = 1 << 0 // Console size (cols, rows) available
	virtioConsoleFMultiport = 1 << 1 // Multiple ports
	virtioConsoleFEmergWr   = 1 << 2 // Emergency write supported
)

type VirtioConsoleDevice struct {
	input  *os.File // host->guest (read from here, inject into RX queue)
	output *os.File // guest->host (write TX queue data here)

	mu     sync.Mutex
	stopCh chan struct{}
}

func NewVirtioConsoleDevice(input, output *os.File) *VirtioConsoleDevice {
	return &VirtioConsoleDevice{
		input:  input,
		output: output,
		stopCh: make(chan struct{}),
	}
}

func (d *VirtioConsoleDevice) DeviceID() uint32 { return virtioDevConsole }
func (d *VirtioConsoleDevice) Tag() string       { return "" }

func (d *VirtioConsoleDevice) Features() uint64 {
	return virtioConsoleFEmergWr
}

func (d *VirtioConsoleDevice) ConfigRead(offset uint32, size uint32) uint32 {
	// struct virtio_console_config {
	//   le16 cols;       // offset 0
	//   le16 rows;       // offset 2
	//   le32 max_nr_ports; // offset 4
	//   le32 emerg_wr;   // offset 8
	// }
	config := make([]byte, 12)
	binary.LittleEndian.PutUint16(config[0:], 80)  // cols
	binary.LittleEndian.PutUint16(config[2:], 25)  // rows
	binary.LittleEndian.PutUint32(config[4:], 1)   // max_nr_ports
	binary.LittleEndian.PutUint32(config[8:], 0)   // emerg_wr

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

func (d *VirtioConsoleDevice) ConfigWrite(offset uint32, size uint32, val uint32) {
	// emerg_wr at offset 8: emergency write — output a character immediately
	if offset == 8 && size == 4 {
		d.output.Write([]byte{byte(val)})
	}
}

func (d *VirtioConsoleDevice) HandleQueue(queueIdx int, dev *virtioMMIODev) {
	switch queueIdx {
	case 0:
		// RX queue — nothing to do; RX is driven by StartRX goroutine
	case 1:
		// TX queue — guest is writing to console
		dev.ProcessAvailRing(queueIdx, func(readBufs, writeBufs [][]byte) uint32 {
			for _, buf := range readBufs {
				d.output.Write(buf)
			}
			return 0
		})
	}
}

// StartRX begins reading from the input pipe and injecting data into
// the guest's RX virtqueue.
func (d *VirtioConsoleDevice) StartRX(dev *virtioMMIODev) {
	go d.rxLoop(dev)
}

func (d *VirtioConsoleDevice) rxLoop(dev *virtioMMIODev) {
	buf := make([]byte, 256)
	for {
		select {
		case <-d.stopCh:
			return
		default:
		}

		n, err := d.input.Read(buf)
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

		data := make([]byte, n)
		copy(data, buf[:n])

		dev.ProcessAvailRing(0, func(readBufs, writeBufs [][]byte) uint32 {
			if len(writeBufs) == 0 {
				return 0
			}
			written := 0
			remaining := data
			for _, wb := range writeBufs {
				cn := copy(wb, remaining)
				written += cn
				remaining = remaining[cn:]
				if len(remaining) == 0 {
					break
				}
			}
			return uint32(written)
		})
	}
}

func (d *VirtioConsoleDevice) Stop() {
	close(d.stopCh)
}
