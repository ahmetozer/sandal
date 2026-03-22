//go:build linux && arm64

package kvm

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	guestMemBase     = 0x40000000 // ARM64 conventional RAM start
	kernelLoadOffset = 0x200000   // ARM64 Image load offset (2MB aligned)

	// ARM64 register IDs for KVM_GET_ONE_REG/KVM_SET_ONE_REG
	// Encoding: 0x6030 0000 00100000 | (register << 0)
	arm64RegBase = 0x6030000000100000
	arm64RegPC   = arm64RegBase | (32 << 1) // PC is register 32 in KVM
	arm64RegX0   = arm64RegBase | 0         // X0
)

// kvmArmVCPUInitStruct corresponds to struct kvm_vcpu_init
type kvmArmVCPUInitStruct struct {
	Target   uint32
	Features [7]uint32
}

// kvmOneReg corresponds to struct kvm_one_reg
type kvmOneReg struct {
	ID   uint64
	Addr uint64
}

type bootConfig struct {
	kernelAddr    uint64
	initrdAddr    uint64
	initrdSize    uint64
	memSize       uint64
	commandLine   string
	numCPUs       uint
	virtioDevices []*virtioMMIODev
}

func setupVM(vmFd int) error {
	// GIC creation deferred to after vCPU creation (required by some kernel versions)
	return nil
}

func createGIC(vmFd int) error {
	// Try GICv3 first
	if err := tryCreateGIC(vmFd, kvmDevTypeARMVGICv3,
		0x08000000, kvmVGICv3AddrTypeDist,
		0x080A0000, kvmVGICv3AddrTypeRedist); err == nil {
		return nil
	}

	// Fall back to GICv2
	return tryCreateGIC(vmFd, kvmDevTypeARMVGICv2,
		0x08000000, kvmVGICv2AddrTypeDist,
		0x08010000, kvmVGICv2AddrTypeCPU)
}

func tryCreateGIC(vmFd int, devType uint32, distAddr uint64, distAttr uint64, cpuAddr uint64, cpuAttr uint64) error {
	dev := kvmCreateDeviceStruct{
		Type: devType,
	}
	if _, err := ioctlPtr(vmFd, kvmCreateDevice, unsafe.Pointer(&dev)); err != nil {
		return err
	}
	gicFd := int(dev.Fd)

	// Set distributor address
	distAddrVal := distAddr
	attr := kvmDeviceAttr{
		Group: kvmDevARMVGICGRPAddr,
		Attr:  distAttr,
		Addr:  uint64(uintptr(unsafe.Pointer(&distAddrVal))),
	}
	if _, err := ioctlPtr(gicFd, kvmSetDeviceAttr, unsafe.Pointer(&attr)); err != nil {
		unix.Close(gicFd)
		return fmt.Errorf("set GIC dist addr: %w", err)
	}

	// Set CPU interface / redistributor address
	cpuAddrVal := cpuAddr
	attr = kvmDeviceAttr{
		Group: kvmDevARMVGICGRPAddr,
		Attr:  cpuAttr,
		Addr:  uint64(uintptr(unsafe.Pointer(&cpuAddrVal))),
	}
	if _, err := ioctlPtr(gicFd, kvmSetDeviceAttr, unsafe.Pointer(&attr)); err != nil {
		unix.Close(gicFd)
		return fmt.Errorf("set GIC cpu addr: %w", err)
	}

	// Initialize the GIC
	attr = kvmDeviceAttr{
		Group: kvmDevARMVGICGRPCtrl,
		Attr:  kvmDevARMVGICCtrlInit,
	}
	if _, err := ioctlPtr(gicFd, kvmSetDeviceAttr, unsafe.Pointer(&attr)); err != nil {
		unix.Close(gicFd)
		return fmt.Errorf("init GIC: %w", err)
	}

	return nil
}

func initVCPUs(vmFd int, vcpuFds []int, mem []byte, boot bootConfig) error {
	// Get preferred target for this host (this is a VM ioctl, not vCPU)
	var initStruct kvmArmVCPUInitStruct
	if _, err := ioctlPtr(vmFd, kvmArmPreferredTarget, unsafe.Pointer(&initStruct)); err != nil {
		return fmt.Errorf("KVM_ARM_PREFERRED_TARGET: %w", err)
	}

	for i, fd := range vcpuFds {
		if _, err := ioctlPtr(fd, kvmArmVCPUInit, unsafe.Pointer(&initStruct)); err != nil {
			return fmt.Errorf("KVM_ARM_VCPU_INIT[%d]: %w", i, err)
		}
	}

	// Create in-kernel GIC (must be after vCPU creation)
	if err := createGIC(vmFd); err != nil {
		return fmt.Errorf("creating GIC: %w", err)
	}

	// Build DTB and place it in memory
	var dtbOffset uint64
	if boot.initrdSize > 0 {
		// Place DTB after initrd (initrdAddr is a guest physical address)
		initrdEnd := boot.initrdAddr - guestMemBase + boot.initrdSize
		dtbOffset = alignUp(initrdEnd, 0x200000)
	} else {
		// If no initrd, place DTB 2MB after kernel
		dtbOffset = alignUp(kernelLoadOffset+0x200000, 0x200000)
	}
	dtbAddr := guestMemBase + dtbOffset

	dtb := buildDTB(boot, dtbAddr)
	if dtbOffset+uint64(len(dtb)) > uint64(len(mem)) {
		return fmt.Errorf("DTB does not fit in memory at offset 0x%x", dtbOffset)
	}
	copy(mem[dtbOffset:], dtb)

	// Set PC to kernel entry point, X0 to DTB address (for first CPU only)
	kernelEntry := uint64(guestMemBase + kernelLoadOffset)
	if err := setOneReg(vcpuFds[0], arm64RegPC, kernelEntry); err != nil {
		return fmt.Errorf("setting PC: %w", err)
	}
	if err := setOneReg(vcpuFds[0], arm64RegX0, dtbAddr); err != nil {
		return fmt.Errorf("setting X0 (DTB addr): %w", err)
	}

	return nil
}

func setOneReg(vcpuFd int, regID, value uint64) error {
	reg := kvmOneReg{
		ID:   regID,
		Addr: uint64(uintptr(unsafe.Pointer(&value))),
	}
	_, err := ioctlPtr(vcpuFd, kvmSetOneReg, unsafe.Pointer(&reg))
	return err
}

// buildDTB creates a minimal Flattened Device Tree for ARM64 boot.
func buildDTB(boot bootConfig, dtbAddr uint64) []byte {
	fdt := newFDTBuilder()

	fdt.beginNode("") // root node
	fdt.propU32("#address-cells", 2)
	fdt.propU32("#size-cells", 2)
	fdt.propString("compatible", "linux,dummy-virt")

	// chosen node
	fdt.beginNode("chosen")
	fdt.propString("bootargs", boot.commandLine)
	fdt.propString("stdout-path", "/pl011@9000000")
	if boot.initrdSize > 0 {
		fdt.propU64("linux,initrd-start", boot.initrdAddr)
		fdt.propU64("linux,initrd-end", boot.initrdAddr+boot.initrdSize)
	}
	fdt.endNode()

	// memory node
	fdt.beginNode("memory@" + fmt.Sprintf("%x", guestMemBase))
	fdt.propString("device_type", "memory")
	fdt.propRegU64(guestMemBase, boot.memSize)
	fdt.endNode()

	// CPUs
	fdt.beginNode("cpus")
	fdt.propU32("#address-cells", 1)
	fdt.propU32("#size-cells", 0)
	for i := uint(0); i < boot.numCPUs; i++ {
		fdt.beginNode(fmt.Sprintf("cpu@%d", i))
		fdt.propString("device_type", "cpu")
		fdt.propString("compatible", "arm,arm-v8")
		fdt.propU32("reg", uint32(i))
		if boot.numCPUs > 1 {
			fdt.propString("enable-method", "psci")
		}
		fdt.endNode()
	}
	fdt.endNode()

	// PSCI node (needed for multi-CPU and power management)
	fdt.beginNode("psci")
	fdt.propString("compatible", "arm,psci-0.2")
	fdt.propString("method", "hvc")
	fdt.endNode()

	// Timer
	fdt.beginNode("timer")
	fdt.propString("compatible", "arm,armv8-timer")
	fdt.propU32("always-on", 1)
	// Interrupts: secure phys, non-secure phys, virt, hyp phys
	fdt.propU32Array("interrupts", []uint32{
		1, 13, 0xf04,
		1, 14, 0xf04,
		1, 11, 0xf04,
		1, 10, 0xf04,
	})
	fdt.endNode()

	// Interrupt controller (GIC v2 minimal)
	fdt.beginNode("intc@8000000")
	fdt.propString("compatible", "arm,cortex-a15-gic")
	fdt.propU32("#interrupt-cells", 3)
	fdt.propEmpty("interrupt-controller")
	fdt.propU32("phandle", 1)
	fdt.propRegPair(0x08000000, 0x10000, 0x08010000, 0x10000)
	fdt.endNode()

	// PL011 UART
	fdt.beginNode("pl011@9000000")
	fdt.propString("compatible", "arm,pl011")
	fdt.propRegU64(pl011Base, pl011Size)
	fdt.propU32Array("interrupts", []uint32{0, 1, 4})
	fdt.endNode()

	// Virtio-MMIO devices
	for i, vdev := range boot.virtioDevices {
		nodeName := fmt.Sprintf("virtio_mmio@%x", vdev.baseAddr)
		fdt.beginNode(nodeName)
		fdt.propString("compatible", "virtio,mmio")
		fdt.propRegU64(vdev.baseAddr, virtioMMIORegionSize)
		// SPI interrupt: type=0 (SPI), number=16+i, flags=edge-triggered
		fdt.propU32Array("interrupts", []uint32{0, uint32(16 + i), 1})
		fdt.propEmpty("dma-coherent")
		fdt.endNode()
	}

	fdt.endNode() // end root

	return fdt.finish()
}

