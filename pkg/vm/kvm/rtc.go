//go:build linux && (amd64 || arm64)

package kvm

import "time"

// PL031 RTC emulation. The kernel reads RTCDR once at boot to
// initialize the wall clock, so this has near-zero ongoing cost.

const (
	pl031Base = 0x09010000
	pl031Size = 0x1000

	// PL031 register offsets
	pl031DR   = 0x000 // Data Register (RO: current Unix timestamp)
	pl031MR   = 0x004 // Match Register
	pl031LR   = 0x008 // Load Register (WO: set time)
	pl031CR   = 0x00C // Control Register
	pl031IMSC = 0x010 // Interrupt Mask Set/Clear
	pl031RIS  = 0x014 // Raw Interrupt Status
	pl031MIS  = 0x018 // Masked Interrupt Status
	pl031ICR  = 0x01C // Interrupt Clear
)

type rtc struct {
	baseTime int64 // Unix seconds at creation
	baseNano int64 // monotonic nanos at creation
}

func newRTC() *rtc {
	now := time.Now()
	return &rtc{
		baseTime: now.Unix(),
		baseNano: now.UnixNano(),
	}
}

// currentTime returns the current Unix timestamp by adding elapsed
// monotonic time to the base time, avoiding repeated syscalls.
func (r *rtc) currentTime() uint32 {
	elapsed := time.Now().UnixNano() - r.baseNano
	return uint32(r.baseTime + elapsed/1e9)
}

func (r *rtc) handleMMIO(addr uint64, length uint32, isWrite uint8, data []byte) bool {
	if addr < pl031Base || addr >= pl031Base+pl031Size {
		return false
	}
	offset := addr - pl031Base

	if isWrite != 0 {
		switch offset {
		case pl031LR:
			// Set time: update base
			val := readMMIOVal(data, length)
			r.baseTime = int64(val)
			r.baseNano = time.Now().UnixNano()
		case pl031CR, pl031IMSC, pl031ICR, pl031MR:
			// Accept writes silently
		}
	} else {
		var val uint32
		switch offset {
		case pl031DR:
			val = r.currentTime()
		case pl031MR:
			val = 0
		case pl031CR:
			val = 1 // RTC enabled
		case pl031RIS, pl031MIS:
			val = 0 // No interrupts pending
		case pl031IMSC:
			val = 0
		default:
			// Peripheral ID and PrimeCell ID registers
			if offset >= 0xFE0 && offset <= 0xFFC {
				val = pl031IDs[(offset-0xFE0)/4]
			}
		}
		writeMMIOVal(data, length, val)
	}
	return true
}

// PL031 identification registers
var pl031IDs = [8]uint32{
	0x31, 0x10, 0x04, 0x00, // PeriphID0-3: PL031 rev 0
	0x0D, 0xF0, 0x05, 0xB1, // PrimeCellID0-3
}
