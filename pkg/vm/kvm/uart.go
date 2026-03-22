//go:build linux

package kvm

import (
	"os"
	"sync"
	"unsafe"
)

// UART emulates a serial port for VM console I/O.
// On ARM64, it handles PL011 MMIO accesses.
// On x86_64, it handles 16550 port I/O accesses.
type uart struct {
	input  *os.File // read guest input from here (host stdin pipe)
	output *os.File // write guest output here (host stdout pipe)

	mu       sync.Mutex
	inputBuf []byte
}

func newUART(input, output *os.File) *uart {
	u := &uart{
		input:  input,
		output: output,
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
	return b, true
}

func (u *uart) writeByte(b byte) {
	u.output.Write([]byte{b})
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
	lsrDataReady   = 0x01
	lsrTHREmpty    = 0x20
	lsrTXEmpty     = 0x40
)

func (u *uart) handleIO(direction uint8, port uint16, size uint8, dataPtr unsafe.Pointer) {
	reg := port - serial16550Port
	if reg > 7 {
		return
	}

	if direction == kvmExitIOOut {
		// Guest writing to UART
		val := *(*uint8)(dataPtr)
		switch reg {
		case uartTHR:
			u.writeByte(val)
		// Other registers: ignore writes for minimal emulation
		}
	} else {
		// Guest reading from UART
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
		// Guest writing
		val := data[0]
		switch offset {
		case pl011DR:
			u.writeByte(val)
		// Other registers: ignore writes for minimal emulation
		}
	} else {
		// Guest reading
		var val uint32
		switch offset {
		case pl011DR:
			if b, ok := u.readByte(); ok {
				val = uint32(b)
			}
		case pl011FR:
			val = pl011FRTxFE // TX FIFO always empty (ready to write)
			if !u.hasInput() {
				val |= pl011FRRxFE // RX FIFO empty
			}
		case pl011CR:
			val = 0x0301 // UART enabled, TX enabled, RX enabled
		case pl011IMSC:
			val = 0
		case pl011RIS, pl011MIS:
			val = 0
		}
		// Write value back based on length
		for i := uint32(0); i < length && i < 4; i++ {
			data[i] = byte(val >> (i * 8))
		}
	}
}
