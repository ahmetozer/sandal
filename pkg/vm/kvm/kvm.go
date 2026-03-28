//go:build linux

package kvm

import (
	"fmt"
	"log/slog"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ioctl numbers for KVM. Derived from linux/kvm.h using the standard
// _IO/_IOW/_IOR/_IOWR encoding: dir(2)<<30 | size(14)<<16 | type(8)<<8 | nr(8).
const (
	// System ioctls (on /dev/kvm fd).
	kvmGetAPIVersion   = 0xAE00     // _IO(0xAE, 0x00)
	kvmCreateVM        = 0xAE01     // _IO(0xAE, 0x01)
	kvmCheckExtension  = 0xAE03     // _IO(0xAE, 0x03)
	kvmGetVCPUMmapSize = 0xAE04     // _IO(0xAE, 0x04)

	// VM ioctls.
	kvmCreateVCPU          = 0xAE41     // _IO(0xAE, 0x41)
	kvmSetUserMemoryRegion = 0x4020AE46 // _IOW(0xAE, 0x46, 32)
	kvmCreateDevice        = 0xC00CAEE0 // _IOWR(0xAE, 0xe0, 12)
	kvmArmPreferredTarget  = 0x8020AEAF // _IOR(0xAE, 0xaf, 32)
	kvmIRQLine             = 0x4008AE61 // _IOW(0xAE, 0x61, 8)
	kvmSetGSIRouting       = 0x4008AE6A // _IOW(0xAE, 0x6A, 8) - header size only; data is variable
	kvmCreateIRQChip       = 0xAE60     // _IO(0xAE, 0x60)
	kvmSetTSSAddr          = 0xAE47     // _IO(0xAE, 0x47)

	// VCPU ioctls.
	kvmRun         = 0xAE80     // _IO(0xAE, 0x80)
	kvmArmVCPUInit = 0x4020AEAE // _IOW(0xAE, 0xae, 32)
	kvmGetOneReg   = 0x4010AEAB // _IOW(0xAE, 0xab, 16)
	kvmSetOneReg   = 0x4010AEAC // _IOW(0xAE, 0xac, 16)
	kvmGetRegs     = 0x8090AE81 // x86 only
	kvmSetRegs     = 0x4090AE82 // x86 only
	kvmGetSregs    = 0x8138AE83 // x86 only
	kvmSetSregs    = 0x4138AE84 // x86 only

	// Device ioctls.
	kvmSetDeviceAttr = 0x4018AEE1 // _IOW(0xAE, 0xe1, 24)

	// KVM exit reasons.
	kvmExitIO          = 2
	kvmExitMMIO        = 6
	kvmExitHLT         = 5
	kvmExitShutdown    = 8
	kvmExitFailEntry   = 9
	kvmExitIntr        = 10
	kvmExitInternalErr = 17
	kvmExitSystemEvent = 24

	// System event types.
	kvmSystemEventShutdown = 1
	kvmSystemEventReset    = 2
	kvmSystemEventCrash    = 3

	// IO direction.
	kvmExitIOIn  = 0
	kvmExitIOOut = 1

	// API version.
	kvmAPIVersion = 12

	// KVM device types.
	kvmDevTypeARMVGICv2 = 5
	kvmDevTypeARMVGICv3 = 7

	// KVM VGIC address types.
	kvmVGICv3AddrTypeDist   = 2
	kvmVGICv3AddrTypeRedist = 3
	kvmVGICv2AddrTypeDist   = 0
	kvmVGICv2AddrTypeCPU    = 1

	// KVM device attribute groups.
	kvmDevARMVGICGRPAddr  = 0
	kvmDevARMVGICGRPNRIRQ = 3
	kvmDevARMVGICGRPCtrl  = 4
	kvmDevARMVGICCtrlInit = 0

	// VCPU signal mask ioctl.
	kvmSetSignalMask = 0x4004AE8B // _IOW(KVMIO, 0x8b, struct kvm_signal_mask)

	// KVM capability constants.
	kvmCapARMVMIPASize = 165

	// ARM64 VCPU feature bits.
	kvmArmVCPUPowerOff = 0
	kvmArmVCPUPSCI02   = 2
)

// kvmUserspaceMemoryRegion corresponds to struct kvm_userspace_memory_region (32 bytes).
type kvmUserspaceMemoryRegion struct {
	Slot          uint32
	Flags         uint32
	GuestPhysAddr uint64
	MemorySize    uint64
	UserspaceAddr uint64
}

// kvmRunExitIO matches the io union member of kvm_run.
type kvmRunExitIO struct {
	Direction  uint8
	Size       uint8
	Port       uint16
	Count      uint32
	DataOffset uint64
}

// kvmRunExitMMIO matches the mmio union member of kvm_run.
type kvmRunExitMMIO struct {
	PhysAddr uint64
	Data     [8]uint8
	Len      uint32
	IsWrite  uint8
}

const kvmRunExitUnionOffset = 32 // offset of the exit union in struct kvm_run

// kvmCreateDeviceStruct corresponds to struct kvm_create_device (12 bytes).
type kvmCreateDeviceStruct struct {
	Type  uint32
	Fd    uint32
	Flags uint32
}

// kvmDeviceAttr corresponds to struct kvm_device_attr (24 bytes).
type kvmDeviceAttr struct {
	Flags uint32
	Group uint32
	Attr  uint64
	Addr  uint64
}

// kvmIRQLevel corresponds to struct kvm_irq_level (8 bytes).
type kvmIRQLevel struct {
	IRQ   uint32
	Level uint32
}

// ARM64 KVM_IRQ_LINE encoding: type(24-27) | vcpu_index(16-23) | irq_number(0-15).
// Type 0 = CPU, Type 1 = SPI, Type 2 = PPI.
// For SPI, irq_number is the full GIC INTID (>= 32), not the SPI index.
const (
	kvmARMIRQTypeSPI   = 1
	kvmARMIRQTypeShift = 24
)

// openKVM opens /dev/kvm (or /devtmpfs/kvm in container environments)
// and verifies the API version.
func openKVM() (int, error) {
	paths := []string{"/dev/kvm", "/devtmpfs/kvm"}
	var fd int
	var err error
	for _, path := range paths {
		fd, err = unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
		if err == nil {
			break
		}
	}
	if err != nil {
		return -1, fmt.Errorf("open /dev/kvm: %w (ensure KVM is available)", err)
	}

	version, err := ioctl(fd, kvmGetAPIVersion, 0)
	if err != nil {
		unix.Close(fd)
		return -1, fmt.Errorf("KVM_GET_API_VERSION: %w", err)
	}
	if version != kvmAPIVersion {
		unix.Close(fd)
		return -1, fmt.Errorf("KVM API version %d, expected %d", version, kvmAPIVersion)
	}

	return fd, nil
}

func ioctl(fd int, request, arg uintptr) (uintptr, error) {
	ret, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(request), arg)
	if errno != 0 {
		return 0, errno
	}
	return ret, nil
}

func ioctlPtr(fd int, request uintptr, arg unsafe.Pointer) (uintptr, error) {
	ret, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), request, uintptr(arg))
	if errno != 0 {
		return 0, errno
	}
	return ret, nil
}

func getVCPUMmapSize(kvmFd int) (int, error) {
	size, err := ioctl(kvmFd, kvmGetVCPUMmapSize, 0)
	if err != nil {
		return 0, fmt.Errorf("KVM_GET_VCPU_MMAP_SIZE: %w", err)
	}
	return int(size), nil
}

// allocateGuestMemory allocates anonymous memory for the guest RAM.
func allocateGuestMemory(size uint64) ([]byte, error) {
	mem, err := unix.Mmap(-1, 0, int(size),
		unix.PROT_READ|unix.PROT_WRITE,
		unix.MAP_PRIVATE|unix.MAP_ANONYMOUS|unix.MAP_NORESERVE)
	if err != nil {
		return nil, fmt.Errorf("mmap guest memory (%d bytes): %w", size, err)
	}
	return mem, nil
}

// loadFileIntoMemory loads a file into guest memory at the given offset.
func loadFileIntoMemory(mem []byte, offset uint64, path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", path, err)
	}
	if offset+uint64(len(data)) > uint64(len(mem)) {
		return 0, fmt.Errorf("file %s (%d bytes) does not fit at offset 0x%x in memory (%d bytes)",
			path, len(data), offset, len(mem))
	}
	copy(mem[offset:], data)
	return uint64(len(data)), nil
}

// setVCPUSignalMask configures KVM_SET_SIGNAL_MASK to block SIGURG (signal 23)
// during KVM_RUN. Go's runtime sends SIGURG for goroutine preemption, which
// interrupts KVM_RUN with EINTR every ~20μs. This prevents the vCPU from
// sleeping in-kernel during WFI (Wait For Interrupt), causing 100% CPU when
// the guest is idle.
//
// The signal mask tells KVM which signals to ALLOW during KVM_RUN.
// We allow only SIGINT (2) and SIGTERM (15) — all others including SIGURG
// are blocked. This matches QEMU's approach in kvm_init_cpu_signals().
func setVCPUSignalMask(vcpuFd int) {
	// struct kvm_signal_mask { __u32 len; __u8 sigset[]; }
	// On ARM64, sigset is 8 bytes (64 signals). The sigset is a bitmask
	// where bit N means signal N+1 is ALLOWED through during KVM_RUN.
	const sigsetSize = 8

	type kvmSignalMask struct {
		Len    uint32
		Sigset [sigsetSize]byte
	}

	mask := kvmSignalMask{Len: sigsetSize}
	// Set bits for signals we want to ALLOW through:
	// SIGINT=2: bit 1 (signal-1) in byte 0
	mask.Sigset[(2-1)/8] |= 1 << ((2 - 1) % 8)
	// SIGTERM=15: bit 6 in byte 1
	mask.Sigset[(15-1)/8] |= 1 << ((15 - 1) % 8)
	// SIGURG=23 is NOT set, so it will be blocked during KVM_RUN.

	if _, err := ioctlPtr(vcpuFd, kvmSetSignalMask, unsafe.Pointer(&mask)); err != nil {
		slog.Warn("KVM_SET_SIGNAL_MASK, idle CPU may be higher", slog.Any("err", err))
	}
}
