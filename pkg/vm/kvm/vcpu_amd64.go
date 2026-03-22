//go:build linux && amd64

package kvm

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	guestMemBase     = 0x0
	kernelLoadOffset = 0x100000 // 1MB - standard Linux kernel load address

	// x86_64 boot protocol addresses
	bootParamsAddr = 0x10000 // Boot params (zero page) at 64KB
	cmdLineAddr    = 0x20000 // Command line at 128KB
	gdt64Addr      = 0x500   // GDT at low memory

	// Linux boot protocol constants
	bootProtocolHeader = 0x53726448 // "HdrS"
	bootTypeLoader     = 0xFF       // Undefined boot loader
)

type bootConfig struct {
	kernelAddr  uint64
	initrdAddr  uint64
	initrdSize  uint64
	memSize     uint64
	commandLine string
	numCPUs     uint
}

// x86_64 segment descriptor
type segmentDesc struct {
	Base     uint64
	Limit    uint32
	Selector uint16
	Type     uint8
	Present  uint8
	DPL      uint8
	DB       uint8
	S        uint8
	L        uint8
	G        uint8
	Avl      uint8
	Unusable uint8
	Padding  uint8
}

// kvmRegs matches struct kvm_regs for x86_64
type kvmRegs struct {
	RAX, RBX, RCX, RDX uint64
	RSI, RDI, RSP, RBP uint64
	R8, R9, R10, R11   uint64
	R12, R13, R14, R15 uint64
	RIP, RFlags         uint64
}

// kvmSregs matches struct kvm_sregs for x86_64
type kvmSregs struct {
	CS, DS, ES, FS, GS, SS       kvmSegment
	TR, LDT                      kvmSegment
	GDT, IDT                     kvmDTable
	CR0, CR2, CR3, CR4            uint64
	CR8                           uint64
	EFER                          uint64
	APICBase                      uint64
	InterruptBitmap               [4]uint64
}

type kvmSegment struct {
	Base     uint64
	Limit    uint32
	Selector uint16
	Type     uint8
	Present  uint8
	DPL      uint8
	DB       uint8
	S        uint8
	L        uint8
	G        uint8
	Avl      uint8
	Unusable uint8
	Padding  uint8
}

type kvmDTable struct {
	Base    uint64
	Limit   uint16
	Padding [3]uint16
}

func setupVM(vmFd int) error {
	// x86 requires setting TSS address and creating IRQ chip for in-kernel PIC
	if _, err := ioctl(vmFd, kvmSetTSSAddr, 0xffffd000); err != nil {
		return fmt.Errorf("KVM_SET_TSS_ADDR: %w", err)
	}
	if _, err := ioctl(vmFd, kvmCreateIRQChip, 0); err != nil {
		return fmt.Errorf("KVM_CREATE_IRQCHIP: %w", err)
	}
	return nil
}

func initVCPUs(vmFd int, vcpuFds []int, mem []byte, boot bootConfig) error {
	// Set up boot parameters (zero page) for Linux boot protocol
	if err := setupBootParams(mem, boot); err != nil {
		return err
	}

	// Set up a minimal 64-bit GDT
	setupGDT64(mem)

	// Configure the first vCPU to start in 64-bit long mode
	if err := setupLongMode(vcpuFds[0], mem, boot); err != nil {
		return err
	}

	return nil
}

func setupBootParams(mem []byte, boot bootConfig) error {
	// Clear boot params area
	for i := 0; i < 4096; i++ {
		mem[bootParamsAddr+i] = 0
	}

	bp := mem[bootParamsAddr:]

	// Setup header at offset 0x1F1 in boot params
	// https://www.kernel.org/doc/html/latest/arch/x86/boot.html
	bp[0x1FE] = 0x55 // boot sector magic
	bp[0x1FF] = 0xAA

	// Header magic "HdrS" at 0x202
	binary.LittleEndian.PutUint32(bp[0x202:], bootProtocolHeader)

	// Boot protocol version 2.14
	binary.LittleEndian.PutUint16(bp[0x206:], 0x020E)

	// Type of loader
	bp[0x210] = bootTypeLoader

	// Loadflags: loaded high (bit 0), keep segments (bit 6), can use heap (bit 7)
	bp[0x211] = 0x01 | 0x40 | 0x80

	// Heap end pointer
	binary.LittleEndian.PutUint16(bp[0x224:], 0xFE00)

	// Command line pointer
	binary.LittleEndian.PutUint32(bp[0x228:], uint32(cmdLineAddr))

	// Copy command line
	cmdBytes := []byte(boot.commandLine)
	if len(cmdBytes) > 255 {
		cmdBytes = cmdBytes[:255]
	}
	copy(mem[cmdLineAddr:], cmdBytes)
	mem[cmdLineAddr+len(cmdBytes)] = 0

	// Initrd address and size
	if boot.initrdSize > 0 {
		binary.LittleEndian.PutUint32(bp[0x218:], uint32(boot.initrdAddr))
		binary.LittleEndian.PutUint32(bp[0x21C:], uint32(boot.initrdSize))
	}

	// E820 memory map at offset 0x2D0 (e820_entries) and 0x2D0+4 = 0x2D0 onwards
	// Number of e820 entries at 0x1E8
	bp[0x1E8] = 1
	// E820 entry at 0x2D0: each entry is 20 bytes (addr:8, size:8, type:4)
	e820 := bp[0x2D0:]
	binary.LittleEndian.PutUint64(e820[0:], 0)             // base address
	binary.LittleEndian.PutUint64(e820[8:], boot.memSize)   // size
	binary.LittleEndian.PutUint32(e820[16:], 1)             // type = RAM

	return nil
}

func setupGDT64(mem []byte) {
	gdt := mem[gdt64Addr:]

	// Null descriptor (entry 0)
	binary.LittleEndian.PutUint64(gdt[0:], 0)

	// Code segment descriptor (entry 1, selector 0x08)
	// Base=0, Limit=0xFFFFF, Type=Execute/Read, S=1, DPL=0, P=1, L=1, G=1
	binary.LittleEndian.PutUint64(gdt[8:], 0x00AF9A000000FFFF)

	// Data segment descriptor (entry 2, selector 0x10)
	// Base=0, Limit=0xFFFFF, Type=Read/Write, S=1, DPL=0, P=1, DB=1, G=1
	binary.LittleEndian.PutUint64(gdt[16:], 0x00CF92000000FFFF)
}

func setupLongMode(vcpuFd int, mem []byte, boot bootConfig) error {
	var sregs kvmSregs
	if _, err := ioctlPtr(vcpuFd, kvmGetSregs, unsafe.Pointer(&sregs)); err != nil {
		return fmt.Errorf("KVM_GET_SREGS: %w", err)
	}

	// Set up page tables for identity mapping
	// PML4 at 0x1000, PDPT at 0x2000, PD at 0x3000
	pml4Addr := uint64(0x1000)
	pdptAddr := uint64(0x2000)
	pdAddr := uint64(0x3000)

	// Clear page table area
	for i := uint64(0); i < 0x4000; i++ {
		mem[pml4Addr+i] = 0
	}

	// PML4[0] -> PDPT (present, writable)
	binary.LittleEndian.PutUint64(mem[pml4Addr:], pdptAddr|0x3)

	// PDPT[0..3] -> PD entries (4 x 1GB entries using 2MB pages)
	for i := uint64(0); i < 4; i++ {
		binary.LittleEndian.PutUint64(mem[pdptAddr+i*8:], (pdAddr+i*0x1000)|0x3)
	}

	// PD entries: identity map first 4GB using 2MB pages
	for i := uint64(0); i < 4*512; i++ {
		binary.LittleEndian.PutUint64(mem[pdAddr+i*8:], (i*0x200000)|0x83) // Present, Writable, PS (2MB page)
	}

	// Enable long mode
	sregs.CR3 = pml4Addr
	sregs.CR4 = 0x20  // PAE
	sregs.CR0 = 0x80050033 // PG, PE, WP, NE, ET, MP
	sregs.EFER = 0x500 // LME | LMA (Long Mode Enable | Long Mode Active)

	// Set up segment registers for 64-bit mode
	codeSeg := kvmSegment{
		Base:     0,
		Limit:    0xFFFFFFFF,
		Selector: 0x08,
		Type:     11, // Execute/Read, accessed
		Present:  1,
		DPL:      0,
		DB:       0,
		S:        1,
		L:        1, // 64-bit mode
		G:        1,
	}
	dataSeg := kvmSegment{
		Base:     0,
		Limit:    0xFFFFFFFF,
		Selector: 0x10,
		Type:     3, // Read/Write, accessed
		Present:  1,
		DPL:      0,
		DB:       1,
		S:        1,
		L:        0,
		G:        1,
	}

	sregs.CS = codeSeg
	sregs.DS = dataSeg
	sregs.ES = dataSeg
	sregs.FS = dataSeg
	sregs.GS = dataSeg
	sregs.SS = dataSeg

	// GDT
	sregs.GDT.Base = gdt64Addr
	sregs.GDT.Limit = 23 // 3 entries * 8 bytes - 1

	if _, err := ioctlPtr(vcpuFd, kvmSetSregs, unsafe.Pointer(&sregs)); err != nil {
		return fmt.Errorf("KVM_SET_SREGS: %w", err)
	}

	// Set general purpose registers
	var regs kvmRegs
	regs.RIP = kernelLoadOffset // Entry point
	regs.RSI = bootParamsAddr   // Pointer to boot params
	regs.RFlags = 0x2           // Reserved bit must be set
	regs.RSP = 0                // Stack not needed for Linux boot

	if _, err := ioctlPtr(vcpuFd, kvmSetRegs, unsafe.Pointer(&regs)); err != nil {
		return fmt.Errorf("KVM_SET_REGS: %w", err)
	}

	return nil
}

// Ensure unix package is used
var _ = unix.EINTR
