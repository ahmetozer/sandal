//go:build linux

package kvm

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// KVM ioctl numbers from linux/kvm.h
const (
	kvmGetAPIVersion       = 0xAE00
	kvmCreateVM            = 0xAE01
	kvmGetVCPUMmapSize     = 0xAE04
	kvmCreateVCPU          = 0xAE41
	kvmSetUserMemoryRegion = 0x4020AE46
	kvmRun                 = 0xAE80
	kvmGetOneReg           = 0x4010AEAB
	kvmSetOneReg           = 0x4010AEAC
	kvmGetRegs             = 0x8090AE81
	kvmSetRegs             = 0x4090AE82
	kvmGetSregs            = 0x8138AE83
	kvmSetSregs            = 0x4138AE84

	// ARM64 specific
	kvmArmPreferredTarget = 0x8020AEAF
	kvmArmVCPUInit        = 0x4020AEAE

	// KVM exit reasons
	kvmExitIO          = 2
	kvmExitMMIO        = 6
	kvmExitShutdown    = 8
	kvmExitSystemEvent = 24
	kvmExitInternalErr = 17


	// KVM VM type
	kvmCreateIRQChip = 0xAE60
	kvmSetTSSAddr    = 0xAE47

	// KVM device management
	kvmCreateDevice = 0xC00CAEE0 // _IOWR(KVMIO, 0xe0, struct kvm_create_device)

	// KVM device types
	kvmDevTypeARMVGICv2 = 5
	kvmDevTypeARMVGICv3 = 7

	// KVM device attributes for VGIC
	kvmDevARMVGICGRPAddr     = 0
	kvmDevARMVGICGRPCtrl     = 4
	kvmVGICv2AddrTypeDist    = 0
	kvmVGICv2AddrTypeCPU     = 1
	kvmVGICv3AddrTypeDist    = 0
	kvmVGICv3AddrTypeRedist  = 1
	kvmDevARMVGICCtrlInit    = 0

	kvmSetDeviceAttr = 0x4018AEE1 // _IOW(KVMIO, 0xe1, struct kvm_device_attr)

	// IO direction
	kvmExitIOIn  = 0
	kvmExitIOOut = 1

	// API version
	kvmAPIVersion = 12
)

// kvmUserspaceMemoryRegion corresponds to struct kvm_userspace_memory_region
type kvmUserspaceMemoryRegion struct {
	Slot          uint32
	Flags         uint32
	GuestPhysAddr uint64
	MemorySize    uint64
	UserspaceAddr uint64
}

// kvmRunExitIO matches the io union member of kvm_run
type kvmRunExitIO struct {
	Direction  uint8
	Size       uint8
	Port       uint16
	Count      uint32
	DataOffset uint64
}

// kvmRunExitMMIO matches the mmio union member of kvm_run
type kvmRunExitMMIO struct {
	PhysAddr uint64
	Data     [8]uint8
	Len      uint32
	IsWrite  uint8
}

const kvmRunExitUnionOffset = 32 // offset of the exit union in struct kvm_run

// kvmCreateDeviceStruct corresponds to struct kvm_create_device
type kvmCreateDeviceStruct struct {
	Type  uint32
	Fd    uint32
	Flags uint32
}

// kvmDeviceAttr corresponds to struct kvm_device_attr
type kvmDeviceAttr struct {
	Flags uint32
	Group uint32
	Attr  uint64
	Addr  uint64
}

// openKVM opens /dev/kvm (or /devtmpfs/kvm in container environments)
// and verifies the API version.
func openKVM() (int, error) {
	// Try standard path first, then container path
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
// Returns the number of bytes loaded.
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
