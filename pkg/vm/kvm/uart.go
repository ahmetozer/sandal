//go:build linux

package kvm

import (
	"os"
	"sync"
	"unsafe"
)

// UART emulates a serial port for VM console I/O.
// On ARM64, it handles PL011 MMIO accesses with proper interrupt support.
// On x86_64, it handles 16550 port I/O accesses.
type uart struct {
	input  *os.File // read guest input from here (host stdin pipe)
	output *os.File // write guest output here (host stdout pipe)
	vmFd   int      // VM fd for KVM_IRQ_LINE

	mu       sync.Mutex
	inputBuf []byte

	// PL011 interrupt state
	intLevel   uint32 // Raw Interrupt Status (RIS)
	intEnabled uint32 // Interrupt Mask Set/Clear (IMSC)
	lcr        uint32 // Line Control Register
	cr         uint32 // Control Register
	irqAsserted bool
}

// PL011 interrupt bits
const (
	pl011IntTX  = 1 << 5 // Transmit interrupt
	pl011IntRX  = 1 << 4 // Receive interrupt
	pl011IntRT  = 1 << 6 // Receive timeout
)

func newUART(input, output *os.File, vmFd int) *uart {
	u := &uart{
		input:  input,
		output: output,
		vmFd:   vmFd,
		cr:     0x0301, // UART enabled, TX/RX enabled
	}
	// Start background reader for input
	go u.readInput()
	return u
}

func (u *uart) readInput() {
	buf := make([]byte, 256)
	for {
		n, err := u.input.Read(buf)
		if err != nil {
			return
		}
		if n > 0 {
			u.mu.Lock()
			u.inputBuf = append(u.inputBuf, buf[:n]...)
			// Set RX interrupt when data arrives
			u.intLevel |= pl011IntRX
			u.updateIRQLocked()
			u.mu.Unlock()
		}
	}
}

func (u *uart) hasInput() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.inputBuf) > 0
}

func (u *uart) readByte() (byte, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.inputBuf) == 0 {
		return 0, false
	}
	b := u.inputBuf[0]
	u.inputBuf = u.inputBuf[1:]
	// Clear RX interrupt if buffer is now empty
	if len(u.inputBuf) == 0 {
		u.intLevel &^= pl011IntRX
	}
	u.updateIRQLocked()
	return b, true
}

func (u *uart) writeByte(b byte) {
	u.output.Write([]byte{b})
	// TX completes immediately — set TX interrupt
	u.mu.Lock()
	u.intLevel |= pl011IntTX
	u.updateIRQLocked()
	u.mu.Unlock()
}

// updateIRQLocked updates the IRQ line based on masked interrupt status.
// Must be called with u.mu held.
func (u *uart) updateIRQLocked() {
	masked := u.intLevel & u.intEnabled
	shouldAssert := masked != 0

	if shouldAssert != u.irqAsserted {
		u.irqAsserted = shouldAssert
		level := uint32(0)
		if shouldAssert {
			level = 1
		}
		// Encode SPI 1 for ARM64 KVM_IRQ_LINE
		irq := kvmIRQLevel{
			IRQ:   (kvmARMIRQTypeSPI << kvmARMIRQTypeShift) | 1, // SPI #1
			Level: level,
		}
		ioctlPtr(u.vmFd, kvmIRQLine, unsafe.Pointer(&irq))
	}
}

// ---- 16550 UART (x86_64 port I/O) ----

const (
	serial16550Port = 0x3F8
	// 16550 register offsets
	uartRBR = 0 // Receive Buffer Register (read)
	uartTHR = 0 // Transmitter Holding Register (write)
	uartIER = 1 // Interrupt Enable Register
	uartIIR = 2 // Interrupt Identification Register (read)
	uartFCR = 2 // FIFO Control Register (write)
	uartLCR = 3 // Line Control Register
	uartMCR = 4 // Modem Control Register
	uartLSR = 5 // Line Status Register
	uartMSR = 6 // Modem Status Register
	uartSCR = 7 // Scratch Register

	// LSR bits
	lsrDataReady = 0x01
	lsrTHREmpty  = 0x20
	lsrTXEmpty   = 0x40
)

func (u *uart) handleIO(direction uint8, port uint16, size uint8, dataPtr unsafe.Pointer) {
	reg := port - serial16550Port
	if reg > 7 {
		return
	}

	if direction == kvmExitIOOut {
		val := *(*uint8)(dataPtr)
		switch reg {
		case uartTHR:
			u.writeByte(val)
		}
	} else {
		var val uint8
		switch reg {
		case uartRBR:
			if b, ok := u.readByte(); ok {
				val = b
			}
		case uartLSR:
			val = lsrTHREmpty | lsrTXEmpty
			if u.hasInput() {
				val |= lsrDataReady
			}
		case uartIIR:
			val = 0x01 // No interrupt pending
		case uartMSR:
			val = 0x00
		}
		*(*uint8)(dataPtr) = val
	}
}

// ---- PL011 UART (ARM64 MMIO) ----

const (
	pl011Base = 0x09000000
	pl011Size = 0x1000

	// PL011 register offsets
	pl011DR   = 0x000 // Data Register
	pl011FR   = 0x018 // Flag Register
	pl011IBRD = 0x024 // Integer Baud Rate
	pl011FBRD = 0x028 // Fractional Baud Rate
	pl011LCRH = 0x02C // Line Control
	pl011CR   = 0x030 // Control Register
	pl011IFLS = 0x034 // Interrupt FIFO Level
	pl011IMSC = 0x038 // Interrupt Mask
	pl011RIS  = 0x03C // Raw Interrupt Status
	pl011MIS  = 0x040 // Masked Interrupt Status
	pl011ICR  = 0x044 // Interrupt Clear

	// Flag register bits
	pl011FRRxFE = 0x10 // Receive FIFO empty
	pl011FRTxFF = 0x20 // Transmit FIFO full
	pl011FRTxFE = 0x80 // Transmit FIFO empty
)

func (u *uart) handleMMIO(addr uint64, length uint32, isWrite uint8, data []byte) {
	if addr < pl011Base || addr >= pl011Base+pl011Size {
		return
	}
	offset := addr - pl011Base

	if isWrite != 0 {
		val := readMMIOVal(data, length)
		u.mu.Lock()
		switch offset {
		case pl011DR:
			u.mu.Unlock()
			u.writeByte(byte(val))
			return
		case pl011IMSC:
			u.intEnabled = val
			u.updateIRQLocked()
		case pl011ICR:
			u.intLevel &^= val
			u.updateIRQLocked()
		case pl011LCRH:
			u.lcr = val
		case pl011CR:
			u.cr = val
		case pl011IBRD, pl011FBRD, pl011IFLS:
			// Ignore baud rate and FIFO level settings
		}
		u.mu.Unlock()
	} else {
		u.mu.Lock()
		var val uint32
		switch offset {
		case pl011DR:
			u.mu.Unlock()
			if b, ok := u.readByte(); ok {
				val = uint32(b)
			}
			writeMMIOVal(data, length, val)
			return
		case pl011FR:
			val = pl011FRTxFE // TX FIFO always empty
			if len(u.inputBuf) == 0 {
				val |= pl011FRRxFE // RX FIFO empty
			}
		case pl011CR:
			val = u.cr
		case pl011LCRH:
			val = u.lcr
		case pl011IMSC:
			val = u.intEnabled
		case pl011RIS:
			val = u.intLevel
		case pl011MIS:
			val = u.intLevel & u.intEnabled
		case pl011IBRD:
			val = 0
		case pl011FBRD:
			val = 0
		case pl011IFLS:
			val = 0
		default:
			// PL011 Peripheral ID and PrimeCell ID registers
			// These are needed for the driver to identify the device
			if offset >= 0xFE0 && offset <= 0xFFC {
				val = pl011IDs[(offset-0xFE0)/4]
			}
		}
		u.mu.Unlock()
		writeMMIOVal(data, length, val)
	}
}

// PL011 identification registers (PeriphID and PrimeCellID)
// These must match what the Linux pl011 driver expects.
var pl011IDs = [8]uint32{
	0x11, 0x10, 0x34, 0x00, // PeriphID0-3: PL011 rev 4
	0x0D, 0xF0, 0x05, 0xB1, // PrimeCellID0-3
}

func readMMIOVal(data []byte, length uint32) uint32 {
	switch length {
	case 1:
		return uint32(data[0])
	case 2:
		return uint32(data[0]) | uint32(data[1])<<8
	case 4:
		return uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24
	}
	return 0
}

func writeMMIOVal(data []byte, length uint32, val uint32) {
	for i := uint32(0); i < length && i < 4; i++ {
		data[i] = byte(val >> (i * 8))
	}
}
