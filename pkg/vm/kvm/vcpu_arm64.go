//go:build linux && arm64

package kvm

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	guestMemBase     = 0x40000000 // ARM64 conventional RAM start (QEMU virt)
	kernelLoadOffset = 0x0        // ARM64 Image load offset at RAM base

	// Standard QEMU virt machine layout.
	gicDistBase   = 0x08000000
	gicCPUBase    = 0x08010000
	gicRedistBase = 0x080A0000
	gicRedistSize = 0x00F60000

	// ARM64 register encoding.
	// ID = KVM_REG_ARM64 | KVM_REG_SIZE_U64 | KVM_REG_ARM_CORE | offset_in_u32_units
	arm64RegBase  = 0x6030000000100000
	arm64RegPC    = arm64RegBase | (32 << 1) // PC is register 32
	arm64RegX0    = arm64RegBase | 0         // X0
	arm64RegPSTATE = arm64RegBase | 0x42     // PSTATE (offset 0x42 in u32 units)

	// PSR_MODE_EL1h with DAIF masked (standard kernel entry state).
	pstateEL1hDAIF = 0x3c5
)

// kvmArmVCPUInitStruct corresponds to struct kvm_vcpu_init (32 bytes).
type kvmArmVCPUInitStruct struct {
	Target   uint32
	Features [7]uint32
}

// kvmOneReg corresponds to struct kvm_one_reg (16 bytes).
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

// ARM VCPU target IDs (from linux/kvm.h).
const (
	armTargetAEMV8        = 0
	armTargetFoundationV8 = 1
	armTargetCortexA57    = 2
	armTargetXgenePotenza = 3
	armTargetCortexA53    = 4
	armTargetGenericV8    = 5
)

var targetCompatMap = map[uint32]string{
	armTargetAEMV8:        "arm,armv8",
	armTargetFoundationV8: "arm,armv8",
	armTargetCortexA57:    "arm,cortex-a57",
	armTargetXgenePotenza: "arm,armv8",
	armTargetCortexA53:    "arm,cortex-a53",
	armTargetGenericV8:    "arm,cortex-a57",
}

func setupVM(vmFd int) error {
	// GIC creation deferred to after vCPU creation (required by some kernel versions)
	return nil
}

// gicVersion tracks which GIC was created.
var gicVersion int // 2 or 3

func createGIC(vmFd int) error {
	// Try GICv3 first.
	if err := createGICv3(vmFd); err == nil {
		gicVersion = 3
		return nil
	}
	// Fallback to GICv2.
	if err := createGICv2(vmFd); err == nil {
		gicVersion = 2
		return nil
	}
	return fmt.Errorf("failed to create GICv3 or GICv2")
}

func createGICv3(vmFd int) error {
	dev := kvmCreateDeviceStruct{Type: kvmDevTypeARMVGICv3}
	if _, err := ioctlPtr(vmFd, kvmCreateDevice, unsafe.Pointer(&dev)); err != nil {
		return err
	}
	gicFd := int(dev.Fd)

	distAddr := uint64(gicDistBase)
	if err := setDeviceAttr(gicFd, kvmDevARMVGICGRPAddr, kvmVGICv3AddrTypeDist, &distAddr); err != nil {
		unix.Close(gicFd)
		return fmt.Errorf("set GICv3 dist addr: %w", err)
	}

	redistAddr := uint64(gicRedistBase)
	if err := setDeviceAttr(gicFd, kvmDevARMVGICGRPAddr, kvmVGICv3AddrTypeRedist, &redistAddr); err != nil {
		unix.Close(gicFd)
		return fmt.Errorf("set GICv3 redist addr: %w", err)
	}

	nrIRQs := uint32(96)
	if err := setDeviceAttr(gicFd, kvmDevARMVGICGRPNRIRQ, 0, &nrIRQs); err != nil {
		unix.Close(gicFd)
		return fmt.Errorf("set GICv3 nr irqs: %w", err)
	}

	if err := setDeviceAttrNoVal(gicFd, kvmDevARMVGICGRPCtrl, kvmDevARMVGICCtrlInit); err != nil {
		unix.Close(gicFd)
		return fmt.Errorf("init GICv3: %w", err)
	}

	return nil
}

func createGICv2(vmFd int) error {
	dev := kvmCreateDeviceStruct{Type: kvmDevTypeARMVGICv2}
	if _, err := ioctlPtr(vmFd, kvmCreateDevice, unsafe.Pointer(&dev)); err != nil {
		return err
	}
	gicFd := int(dev.Fd)

	distAddr := uint64(gicDistBase)
	if err := setDeviceAttr(gicFd, kvmDevARMVGICGRPAddr, kvmVGICv2AddrTypeDist, &distAddr); err != nil {
		unix.Close(gicFd)
		return fmt.Errorf("set GICv2 dist addr: %w", err)
	}

	cpuAddr := uint64(gicCPUBase)
	if err := setDeviceAttr(gicFd, kvmDevARMVGICGRPAddr, kvmVGICv2AddrTypeCPU, &cpuAddr); err != nil {
		unix.Close(gicFd)
		return fmt.Errorf("set GICv2 cpu addr: %w", err)
	}

	nrIRQs := uint32(96)
	if err := setDeviceAttr(gicFd, kvmDevARMVGICGRPNRIRQ, 0, &nrIRQs); err != nil {
		unix.Close(gicFd)
		return fmt.Errorf("set GICv2 nr irqs: %w", err)
	}

	if err := setDeviceAttrNoVal(gicFd, kvmDevARMVGICGRPCtrl, kvmDevARMVGICCtrlInit); err != nil {
		unix.Close(gicFd)
		return fmt.Errorf("init GICv2: %w", err)
	}

	return nil
}

func setDeviceAttr(devFd int, group uint32, attr uint64, valPtr interface{}) error {
	var addr uint64
	switch v := valPtr.(type) {
	case *uint64:
		addr = uint64(uintptr(unsafe.Pointer(v)))
	case *uint32:
		addr = uint64(uintptr(unsafe.Pointer(v)))
	}
	a := kvmDeviceAttr{
		Group: group,
		Attr:  attr,
		Addr:  addr,
	}
	_, err := ioctlPtr(devFd, kvmSetDeviceAttr, unsafe.Pointer(&a))
	return err
}

func setDeviceAttrNoVal(devFd int, group uint32, attr uint64) error {
	a := kvmDeviceAttr{
		Group: group,
		Attr:  attr,
	}
	_, err := ioctlPtr(devFd, kvmSetDeviceAttr, unsafe.Pointer(&a))
	return err
}

// detectCPUCompat queries the preferred target and returns the DTB-compatible string.
func detectCPUCompat(vmFd int) string {
	var initStruct kvmArmVCPUInitStruct
	if _, err := ioctlPtr(vmFd, kvmArmPreferredTarget, unsafe.Pointer(&initStruct)); err != nil {
		return "arm,cortex-a57" // safe fallback
	}
	if compat, ok := targetCompatMap[initStruct.Target]; ok {
		return compat
	}
	return "arm,cortex-a57"
}

func initVCPUs(vmFd int, vcpuFds []int, mem []byte, boot bootConfig) error {
	// Get preferred target for this host.
	var initStruct kvmArmVCPUInitStruct
	if _, err := ioctlPtr(vmFd, kvmArmPreferredTarget, unsafe.Pointer(&initStruct)); err != nil {
		return fmt.Errorf("KVM_ARM_PREFERRED_TARGET: %w", err)
	}
	// Enable PSCI v0.2+ for proper CPU suspend/idle and power management.
	initStruct.Features[0] |= 1 << kvmArmVCPUPSCI02

	for i, fd := range vcpuFds {
		cpuInit := initStruct
		if i > 0 {
			// Secondary CPUs must start in POWER_OFF state.
			// Primary CPU will bring them online via PSCI CPU_ON.
			cpuInit.Features[0] |= 1 << kvmArmVCPUPowerOff
		}
		if _, err := ioctlPtr(fd, kvmArmVCPUInit, unsafe.Pointer(&cpuInit)); err != nil {
			return fmt.Errorf("KVM_ARM_VCPU_INIT[%d]: %w", i, err)
		}
	}

	// Create in-kernel GIC (must be after vCPU creation).
	if err := createGIC(vmFd); err != nil {
		return fmt.Errorf("creating GIC: %w", err)
	}

	// Detect CPU compatible string for DTB.
	cpuCompat := detectCPUCompat(vmFd)

	// Build DTB and place it in memory.
	// Use fixed offset at 48MB into RAM (matching QEMU virt layout).
	dtbOffset := uint64(48 * 1024 * 1024)
	dtbAddr := guestMemBase + dtbOffset

	dtb := buildDTB(boot, dtbAddr, cpuCompat)
	if dtbOffset+uint64(len(dtb)) > uint64(len(mem)) {
		return fmt.Errorf("DTB does not fit in memory at offset 0x%x", dtbOffset)
	}
	copy(mem[dtbOffset:], dtb)

	// Set initial register state for kernel boot (first CPU only).
	// X0 = DTB address, PC = kernel entry, PSTATE = EL1h with DAIF masked.
	kernelEntry := uint64(guestMemBase + kernelLoadOffset)
	if err := setOneReg(vcpuFds[0], arm64RegPC, kernelEntry); err != nil {
		return fmt.Errorf("setting PC: %w", err)
	}
	if err := setOneReg(vcpuFds[0], arm64RegX0, dtbAddr); err != nil {
		return fmt.Errorf("setting X0 (DTB addr): %w", err)
	}
	if err := setOneReg(vcpuFds[0], arm64RegPSTATE, pstateEL1hDAIF); err != nil {
		return fmt.Errorf("setting PSTATE: %w", err)
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

// buildDTB creates a device tree for ARM64 boot, matching the QEMU virt machine layout.
func buildDTB(boot bootConfig, dtbAddr uint64, cpuCompat string) []byte {
	fdt := newFDTBuilder()

	fdt.beginNode("") // root node
	fdt.propString("compatible", "linux,dummy-virt")
	fdt.propU32("#address-cells", 2)
	fdt.propU32("#size-cells", 2)
	fdt.propU32("interrupt-parent", 1) // phandle of GIC

	// Memory node.
	fdt.beginNode(fmt.Sprintf("memory@%x", guestMemBase))
	fdt.propString("device_type", "memory")
	fdt.propU32Array("reg", []uint32{
		uint32(guestMemBase >> 32), uint32(guestMemBase),
		uint32(boot.memSize >> 32), uint32(boot.memSize),
	})
	fdt.endNode()

	// Chosen node.
	fdt.beginNode("chosen")
	fdt.propString("bootargs", boot.commandLine)
	fdt.propString("stdout-path", "/pl011@9000000")
	if boot.initrdSize > 0 {
		fdt.propU64("linux,initrd-start", boot.initrdAddr)
		fdt.propU64("linux,initrd-end", boot.initrdAddr+boot.initrdSize)
	}
	fdt.endNode()

	// PSCI node.
	fdt.beginNode("psci")
	fdt.propString("compatible", "arm,psci-0.2")
	fdt.propString("method", "hvc")
	fdt.endNode()

	// CPUs node.
	fdt.beginNode("cpus")
	fdt.propU32("#address-cells", 1)
	fdt.propU32("#size-cells", 0)
	for i := uint(0); i < boot.numCPUs; i++ {
		fdt.beginNode(fmt.Sprintf("cpu@%d", i))
		fdt.propString("device_type", "cpu")
		fdt.propString("compatible", cpuCompat)
		fdt.propU32("reg", uint32(i))
		fdt.propString("enable-method", "psci")
		fdt.endNode()
	}
	fdt.endNode()

	// Fixed clock (required by PL011).
	fdt.beginNode("apb-pclk")
	fdt.propString("compatible", "fixed-clock")
	fdt.propU32("#clock-cells", 0)
	fdt.propU32("clock-frequency", 24000000)
	fdt.propU32("phandle", 2) // clock phandle
	fdt.endNode()

	// GIC interrupt controller.
	fdt.beginNode(fmt.Sprintf("intc@%x", gicDistBase))
	if gicVersion == 3 {
		fdt.propString("compatible", "arm,gic-v3")
		fdt.propU32("#interrupt-cells", 3)
		fdt.propEmpty("interrupt-controller")
		fdt.propU32("#address-cells", 2)
		fdt.propU32("#size-cells", 2)
		fdt.propEmpty("ranges")
		fdt.propU32Array("reg", []uint32{
			uint32(gicDistBase >> 32), uint32(gicDistBase), 0, 0x10000,
			uint32(gicRedistBase >> 32), uint32(gicRedistBase),
			uint32(gicRedistSize >> 32), uint32(gicRedistSize),
		})
	} else {
		fdt.propString("compatible", "arm,cortex-a15-gic")
		fdt.propU32("#interrupt-cells", 3)
		fdt.propEmpty("interrupt-controller")
		fdt.propU32("#address-cells", 2)
		fdt.propU32("#size-cells", 2)
		fdt.propEmpty("ranges")
		fdt.propU32Array("reg", []uint32{
			uint32(gicDistBase >> 32), uint32(gicDistBase), 0, 0x10000,
			uint32(gicCPUBase >> 32), uint32(gicCPUBase), 0, 0x10000,
		})
	}
	fdt.propU32("phandle", 1) // GIC phandle
	fdt.endNode()

	// Timer node.
	fdt.beginNode("timer")
	fdt.propStringList("compatible", []string{"arm,armv8-timer", "arm,armv7-timer"})
	fdt.propEmpty("always-on")
	// Interrupts: type=1 (PPI), IRQ numbers, flags=4 (IRQ_TYPE_LEVEL_HIGH)
	fdt.propU32Array("interrupts", []uint32{
		1, 13, 4, // Secure EL1 phys timer (PPI 29)
		1, 14, 4, // Non-secure EL1 phys timer (PPI 30)
		1, 11, 4, // Virtual timer (PPI 27)
		1, 10, 4, // Non-secure EL2 phys timer (PPI 26)
	})
	fdt.endNode()

	// PL011 UART.
	fdt.beginNode("pl011@9000000")
	fdt.propStringList("compatible", []string{"arm,pl011", "arm,primecell"})
	fdt.propU32Array("reg", []uint32{0, 0x09000000, 0, 0x1000})
	fdt.propU32Array("interrupts", []uint32{0, 1, 4}) // SPI 1, level-high
	fdt.propU32("interrupt-parent", 1)
	fdt.propStringList("clock-names", []string{"uartclk", "apb_pclk"})
	fdt.propU32Array("clocks", []uint32{2, 2})
	fdt.endNode()

	// Virtio-MMIO devices.
	for i, vdev := range boot.virtioDevices {
		nodeName := fmt.Sprintf("virtio_mmio@%x", vdev.baseAddr)
		fdt.beginNode(nodeName)
		fdt.propString("compatible", "virtio,mmio")
		fdt.propU32Array("reg", []uint32{
			uint32(vdev.baseAddr >> 32), uint32(vdev.baseAddr),
			0, virtioMMIORegionSize,
		})
		fdt.propU32Array("interrupts", []uint32{0, uint32(16 + i), 1}) // SPI, edge-rising
		fdt.propU32("interrupt-parent", 1)
		fdt.propEmpty("dma-coherent")
		fdt.endNode()
	}

	fdt.endNode() // end root

	return fdt.finish()
}
