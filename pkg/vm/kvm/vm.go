//go:build linux

package kvm

import (
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"unsafe"

	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"golang.org/x/sys/unix"
)

type VMState int

const (
	VMStateStopped  VMState = 0
	VMStateRunning  VMState = 1
	VMStatePaused   VMState = 2
	VMStateError    VMState = 3
	VMStateStarting VMState = 4
	VMStateStopping VMState = 7
)

func (s VMState) String() string {
	switch s {
	case VMStateStopped:
		return "stopped"
	case VMStateRunning:
		return "running"
	case VMStatePaused:
		return "paused"
	case VMStateError:
		return "error"
	case VMStateStarting:
		return "starting"
	case VMStateStopping:
		return "stopping"
	default:
		return "unknown"
	}
}

type VM struct {
	Name   string
	Config vmconfig.VMConfig

	kvmFd   int
	vmFd    int
	vcpuFds  []int
	vcpuRun  [][]byte // mmap'd kvm_run regions per vCPU
	vcpuTids []int    // OS thread IDs for signal-based wakeup

	memory []byte // guest physical RAM

	stdinWriter  *os.File // host writes here -> guest reads
	stdoutReader *os.File // guest writes -> host reads here
	stdinReader  *os.File // UART reads from here
	stdoutWriter *os.File // UART writes here

	state      VMState
	mu         sync.Mutex
	stopCh     chan struct{}
	stoppedCh  chan error
	uart       *uart
	rtc        *rtc
	virtioDevs []*virtioMMIODev
	consoleDev *VirtioConsoleDevice
	netDevs    []*VirtioNetDevice
	blkDevs    []*VirtioBlkDevice
	vsockDev   *VirtioVsockDevice
	tap        *tapDevice
}

func NewVM(name string, cfg vmconfig.VMConfig) (*VM, error) {
	kvmFd, err := openKVM()
	if err != nil {
		return nil, err
	}

	// Create VM.
	vmFd, err := ioctl(kvmFd, kvmCreateVM, 0)
	if err != nil {
		unix.Close(kvmFd)
		return nil, fmt.Errorf("KVM_CREATE_VM: %w", err)
	}

	// Platform-specific VM setup (IRQ chip on x86, noop on ARM64).
	if err := setupVM(int(vmFd)); err != nil {
		unix.Close(int(vmFd))
		unix.Close(kvmFd)
		return nil, fmt.Errorf("VM setup: %w", err)
	}

	// Allocate guest memory with MAP_NORESERVE (allows overcommit).
	mem, err := allocateGuestMemory(cfg.MemoryBytes)
	if err != nil {
		unix.Close(int(vmFd))
		unix.Close(kvmFd)
		return nil, err
	}

	// Register guest memory with KVM.
	region := kvmUserspaceMemoryRegion{
		Slot:          0,
		GuestPhysAddr: guestMemBase,
		MemorySize:    cfg.MemoryBytes,
		UserspaceAddr: uint64(uintptr(unsafe.Pointer(&mem[0]))),
	}
	if _, err := ioctlPtr(int(vmFd), kvmSetUserMemoryRegion, unsafe.Pointer(&region)); err != nil {
		unix.Munmap(mem)
		unix.Close(int(vmFd))
		unix.Close(kvmFd)
		return nil, fmt.Errorf("KVM_SET_USER_MEMORY_REGION: %w", err)
	}

	// Load kernel into guest memory.
	kernelSize, err := loadFileIntoMemory(mem, kernelLoadOffset, cfg.KernelPath)
	if err != nil {
		unix.Munmap(mem)
		unix.Close(int(vmFd))
		unix.Close(kvmFd)
		return nil, fmt.Errorf("loading kernel: %w", err)
	}

	// Load initrd if provided.
	// Place initrd at 50MB offset (matching QEMU virt layout).
	var initrdAddr, initrdSize uint64
	if cfg.InitrdPath != "" {
		initrdAddr = 50 * 1024 * 1024
		if initrdAddr < kernelLoadOffset+kernelSize {
			initrdAddr = alignUp(kernelLoadOffset+kernelSize, 0x1000)
		}
		initrdSize, err = loadFileIntoMemory(mem, initrdAddr, cfg.InitrdPath)
		if err != nil {
			unix.Munmap(mem)
			unix.Close(int(vmFd))
			unix.Close(kvmFd)
			return nil, fmt.Errorf("loading initrd: %w", err)
		}
	}

	// Create serial console pipes.
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		unix.Munmap(mem)
		unix.Close(int(vmFd))
		unix.Close(kvmFd)
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		unix.Munmap(mem)
		unix.Close(int(vmFd))
		unix.Close(kvmFd)
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	u := newUART(stdinReader, stdoutWriter, int(vmFd))
	rtcDev := newRTC()

	// Get vCPU mmap size.
	vcpuMmapSize, err := getVCPUMmapSize(kvmFd)
	if err != nil {
		unix.Munmap(mem)
		unix.Close(int(vmFd))
		unix.Close(kvmFd)
		return nil, err
	}

	// Create vCPUs.
	vcpuFds := make([]int, cfg.CPUCount)
	vcpuTids := make([]int, cfg.CPUCount)
	vcpuRun := make([][]byte, cfg.CPUCount)
	for i := uint(0); i < cfg.CPUCount; i++ {
		vcpuFd, err := ioctl(int(vmFd), kvmCreateVCPU, uintptr(i))
		if err != nil {
			for j := uint(0); j < i; j++ {
				unix.Close(vcpuFds[j])
			}
			unix.Munmap(mem)
			unix.Close(int(vmFd))
			unix.Close(kvmFd)
			return nil, fmt.Errorf("KVM_CREATE_VCPU[%d]: %w", i, err)
		}
		vcpuFds[i] = int(vcpuFd)

		run, err := unix.Mmap(vcpuFds[i], 0, vcpuMmapSize,
			unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
		if err != nil {
			for j := uint(0); j <= i; j++ {
				unix.Close(vcpuFds[j])
			}
			unix.Munmap(mem)
			unix.Close(int(vmFd))
			unix.Close(kvmFd)
			return nil, fmt.Errorf("mmap kvm_run[%d]: %w", i, err)
		}
		vcpuRun[i] = run
	}

	// Create virtio devices.
	var virtioDevs []*virtioMMIODev
	var netDevs []*VirtioNetDevice
	var consoleDev *VirtioConsoleDevice
	devIdx := 0

	// Virtio-console device — provides /dev/hvc0.
	// Uses its own pipe pair so it doesn't compete with the PL011 UART
	// for host stdin bytes. On Linux ARM64, the console is ttyAMA0 (UART),
	// so hvc0 is secondary and shouldn't steal input from the UART.
	{
		consoleInR, consoleInW, err := os.Pipe()
		if err != nil {
			unix.Munmap(mem)
			unix.Close(int(vmFd))
			unix.Close(kvmFd)
			return nil, fmt.Errorf("console stdin pipe: %w", err)
		}
		consoleOutR, consoleOutW, err := os.Pipe()
		if err != nil {
			unix.Munmap(mem)
			unix.Close(int(vmFd))
			unix.Close(kvmFd)
			return nil, fmt.Errorf("console stdout pipe: %w", err)
		}
		// Close write end of console input — nothing feeds hvc0 input.
		consoleInW.Close()
		// Close read end of console output — hvc0 output is discarded.
		consoleOutR.Close()
		consoleDev = NewVirtioConsoleDevice(consoleInR, consoleOutW)
		baseAddr := uint64(0x0a000000) + uint64(devIdx)*virtioMMIORegionSize
		irqNum := uint32(16 + devIdx)
		vDev := newVirtioMMIODev(baseAddr, irqNum, int(vmFd), mem, consoleDev)
		virtioDevs = append(virtioDevs, vDev)
		devIdx++
	}

	// Virtio-blk devices for disk and ISO images.
	var blkDevs []*VirtioBlkDevice
	if cfg.DiskPath != "" {
		blkDev, err := NewVirtioBlkDevice(cfg.DiskPath, false)
		if err != nil {
			slog.Warn("failed to open disk", slog.String("path", cfg.DiskPath), slog.Any("err", err))
		} else {
			baseAddr := uint64(0x0a000000) + uint64(devIdx)*virtioMMIORegionSize
			irqNum := uint32(16 + devIdx)
			vDev := newVirtioMMIODev(baseAddr, irqNum, int(vmFd), mem, blkDev)
			virtioDevs = append(virtioDevs, vDev)
			blkDevs = append(blkDevs, blkDev)
			devIdx++
		}
	}
	if cfg.ISOPath != "" {
		isoDev, err := NewVirtioBlkDevice(cfg.ISOPath, true)
		if err != nil {
			slog.Warn("failed to open ISO", slog.String("path", cfg.ISOPath), slog.Any("err", err))
		} else {
			baseAddr := uint64(0x0a000000) + uint64(devIdx)*virtioMMIORegionSize
			irqNum := uint32(16 + devIdx)
			vDev := newVirtioMMIODev(baseAddr, irqNum, int(vmFd), mem, isoDev)
			virtioDevs = append(virtioDevs, vDev)
			blkDevs = append(blkDevs, isoDev)
			devIdx++
		}
	}

	// Virtio-net device backed by TAP interface.
	// Placed before VirtioFS so the kernel assigns eth0 to it.
	tapName := fmt.Sprintf("sandal%d", os.Getpid()%10000)
	tap, err := createTAP(tapName)
	if err != nil {
		slog.Warn("failed to create TAP device, networking disabled", slog.Any("err", err))
	} else {
		// Attach TAP to the sandal0 bridge (same network as containers).
		if err := tap.attachToBridge(); err != nil {
			slog.Warn("bridge attachment failed, networking may not work", slog.Any("err", err))
		}

		mac := [6]byte{0x52, 0x54, 0x00, byte(os.Getpid() >> 8), byte(os.Getpid()), 0x01}
		netDev := NewVirtioNetDevice(tap, mac)
		baseAddr := uint64(0x0a000000) + uint64(devIdx)*virtioMMIORegionSize
		irqNum := uint32(16 + devIdx)
		vDev := newVirtioMMIODev(baseAddr, irqNum, int(vmFd), mem, netDev)
		virtioDevs = append(virtioDevs, vDev)
		netDevs = append(netDevs, netDev)
		devIdx++
	}

	// VirtioFS devices for each mount.
	for _, mount := range cfg.Mounts {
		fsDev := NewVirtioFSDevice(mount.Tag, mount.HostPath, mount.ReadOnly)
		baseAddr := uint64(0x0a000000) + uint64(devIdx)*virtioMMIORegionSize
		irqNum := uint32(16 + devIdx)
		vDev := newVirtioMMIODev(baseAddr, irqNum, int(vmFd), mem, fsDev)
		virtioDevs = append(virtioDevs, vDev)
		devIdx++
	}

	// Virtio-vsock device for host<->guest socket communication.
	vsockDev := NewVirtioVsockDevice(3) // CID=3 (guest)
	{
		baseAddr := uint64(0x0a000000) + uint64(devIdx)*virtioMMIORegionSize
		irqNum := uint32(16 + devIdx)
		vDev := newVirtioMMIODev(baseAddr, irqNum, int(vmFd), mem, vsockDev)
		virtioDevs = append(virtioDevs, vDev)
		devIdx++
	}

	// Initialize vCPU registers (architecture-specific).
	bootParams := bootConfig{
		kernelAddr:    guestMemBase + kernelLoadOffset,
		initrdAddr:    guestMemBase + initrdAddr,
		initrdSize:    initrdSize,
		memSize:       cfg.MemoryBytes,
		commandLine:   cfg.CommandLine,
		numCPUs:       cfg.CPUCount,
		virtioDevices: virtioDevs,
	}

	if err := initVCPUs(int(vmFd), vcpuFds, mem, bootParams); err != nil {
		for i := range vcpuFds {
			unix.Munmap(vcpuRun[i])
			unix.Close(vcpuFds[i])
		}
		unix.Munmap(mem)
		unix.Close(int(vmFd))
		unix.Close(kvmFd)
		return nil, fmt.Errorf("init vCPUs: %w", err)
	}

	// Set up GSI routing so KVM_IRQFD can deliver interrupts.
	// On ARM64, the generic kvm_irq_map_gsi uses kvm->irq_routing which
	// is NULL until KVM_SET_GSI_ROUTING is called. Without routing,
	// IRQFD eventfd writes are silently dropped.
	if err := setupGSIRouting(int(vmFd), virtioDevs); err != nil {
		slog.Warn("GSI routing setup failed", slog.Any("err", err))
	}

	// Register eventfd-based IRQ injection for each virtio device.
	// Must happen after initVCPUs (creates GIC) and GSI routing setup.
	for _, vdev := range virtioDevs {
		vdev.setupIRQFD()
	}

	return &VM{
		Name:         name,
		Config:       cfg,
		kvmFd:        kvmFd,
		vmFd:         int(vmFd),
		vcpuFds:      vcpuFds,
		vcpuTids:     vcpuTids,
		vcpuRun:      vcpuRun,
		memory:       mem,
		stdinWriter:  stdinWriter,
		stdoutReader: stdoutReader,
		stdinReader:  stdinReader,
		stdoutWriter: stdoutWriter,
		state:        VMStateStopped,
		stopCh:       make(chan struct{}),
		stoppedCh:    make(chan error, 1),
		uart:         u,
		rtc:          rtcDev,
		virtioDevs:   virtioDevs,
		consoleDev:   consoleDev,
		netDevs:      netDevs,
		blkDevs:      blkDevs,
		vsockDev:     vsockDev,
		tap:          tap,
	}, nil
}

func (vm *VM) Start() error {
	vm.mu.Lock()
	if vm.state != VMStateStopped {
		vm.mu.Unlock()
		return fmt.Errorf("VM is in state %s, cannot start", vm.state)
	}
	vm.state = VMStateStarting
	vm.mu.Unlock()

	var wg sync.WaitGroup

	for i := range vm.vcpuFds {
		wg.Add(1)
		go func(cpuID int) {
			defer wg.Done()
			vm.runVCPU(cpuID)
		}(i)
	}

	// Start I/O loops for virtio devices.
	// Note: VirtioNetDevice I/O is started from the DRIVER_OK handler
	// (after vhost-net setup is attempted), not here.
	for _, vd := range vm.virtioDevs {
		if dev, ok := vd.device.(*VirtioConsoleDevice); ok {
			dev.StartRX(vd)
		}
	}

	vm.mu.Lock()
	vm.state = VMStateRunning
	vm.mu.Unlock()

	go func() {
		wg.Wait()
		for _, nd := range vm.netDevs {
			nd.Stop()
		}
		vm.mu.Lock()
		vm.state = VMStateStopped
		vm.mu.Unlock()
		vm.stoppedCh <- nil
	}()

	return nil
}

// runVCPU executes the KVM_RUN loop for a single vCPU.
// Modeled after QEMU's kvm_cpu_exec() in accel/kvm/kvm-all.c.
func (vm *VM) runVCPU(cpuID int) {
	// Pin this goroutine to an OS thread so KVM_SET_SIGNAL_MASK applies
	// consistently and the vCPU fd stays on the same thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Record OS thread ID so Stop() can send a signal to interrupt KVM_RUN.
	vm.vcpuTids[cpuID] = unix.Gettid()

	fd := vm.vcpuFds[cpuID]
	run := vm.vcpuRun[cpuID]

	// Block SIGURG (Go's goroutine preemption signal, #23) during KVM_RUN.
	// Without this, Go's sysmon sends SIGURG every ~20μs to preempt goroutines,
	// which interrupts KVM_RUN with EINTR. This prevents the vCPU from sleeping
	// in-kernel during WFI, causing ~100% CPU per vCPU when the guest is idle.
	// This matches QEMU's kvm_init_cpu_signals() approach.
	setVCPUSignalMask(fd)

	for {
		select {
		case <-vm.stopCh:
			return
		default:
		}

		_, err := ioctl(fd, kvmRun, 0)
		if err != nil {
			if err == unix.EINTR || err == unix.EAGAIN || err == unix.EBADF {
				select {
				case <-vm.stopCh:
					return
				default:
					if err == unix.EBADF {
						return // fd was closed by Stop()
					}
					continue
				}
			}
			slog.Error("KVM_RUN", slog.Int("vcpu", cpuID), slog.Any("err", err))
			return
		}

		exitReason := *(*uint32)(unsafe.Pointer(&run[8]))

		switch exitReason {
		case kvmExitIO:
			vm.handleExitIO(run)
		case kvmExitMMIO:
			vm.handleExitMMIO(run)
		case kvmExitHLT:
			// Guest executed HLT. With in-kernel GIC and PSCI v0.2+,
			// WFI is handled in-kernel. If HLT exits reach here, the
			// guest is done.
			vm.Stop()
			return
		case kvmExitShutdown:
			vm.Stop()
			return
		case kvmExitSystemEvent:
			evType := *(*uint32)(unsafe.Pointer(&run[kvmRunExitUnionOffset]))
			switch evType {
			case kvmSystemEventShutdown:
				vm.Stop()
				return
			case kvmSystemEventReset:
				slog.Warn("guest requested reset", slog.Int("vcpu", cpuID))
				vm.Stop()
				return
			case kvmSystemEventCrash:
				slog.Error("guest crashed", slog.Int("vcpu", cpuID))
				vm.Stop()
				return
			}
		case kvmExitFailEntry:
			slog.Error("vCPU fail entry", slog.Int("vcpu", cpuID))
			return
		case kvmExitInternalErr:
			suberror := *(*uint32)(unsafe.Pointer(&run[kvmRunExitUnionOffset]))
			slog.Error("KVM internal error", slog.Int("vcpu", cpuID), slog.Int("suberror", int(suberror)))
			return
		case kvmExitIntr:
			// Interrupted by signal, continue.
			continue
		default:
			slog.Error("unhandled exit reason", slog.Int("vcpu", cpuID), slog.Int("reason", int(exitReason)))
			return
		}
	}
}

func (vm *VM) handleExitIO(run []byte) {
	exitIO := (*kvmRunExitIO)(unsafe.Pointer(&run[kvmRunExitUnionOffset]))
	dataPtr := unsafe.Pointer(&run[exitIO.DataOffset])
	vm.uart.handleIO(exitIO.Direction, exitIO.Port, exitIO.Size, dataPtr)
}

func (vm *VM) handleExitMMIO(run []byte) {
	exitMMIO := (*kvmRunExitMMIO)(unsafe.Pointer(&run[kvmRunExitUnionOffset]))

	// Try virtio devices first.
	for _, vdev := range vm.virtioDevs {
		if vdev.HandleMMIO(exitMMIO.PhysAddr, exitMMIO.Len, exitMMIO.IsWrite, exitMMIO.Data[:]) {
			return
		}
	}

	// Try RTC.
	if vm.rtc.handleMMIO(exitMMIO.PhysAddr, exitMMIO.Len, exitMMIO.IsWrite, exitMMIO.Data[:]) {
		return
	}

	// Fall back to UART.
	vm.uart.handleMMIO(exitMMIO.PhysAddr, exitMMIO.Len, exitMMIO.IsWrite, exitMMIO.Data[:])
}

func (vm *VM) Stop() error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if vm.state != VMStateRunning {
		return nil
	}
	vm.state = VMStateStopping
	close(vm.stopCh)
	// Wake vCPU threads that may be blocked in KVM_RUN (including
	// in-kernel sleep from PSCI CPU_OFF). Closing fds alone doesn't
	// reliably interrupt a blocked ioctl; a signal is needed.
	// SIGURG is allowed through the vCPU signal mask, so it causes
	// KVM_RUN to return with EINTR. The loop then sees stopCh closed.
	pid := os.Getpid()
	for _, tid := range vm.vcpuTids {
		if tid > 0 {
			unix.Tgkill(pid, tid, unix.SIGURG)
		}
	}
	for i, fd := range vm.vcpuFds {
		if fd >= 0 {
			unix.Close(fd)
			vm.vcpuFds[i] = -1
		}
	}
	return nil
}

func (vm *VM) RequestStop() {
	vm.Stop()
}

func (vm *VM) State() VMState {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	return vm.state
}

func (vm *VM) WaitUntilStopped() error {
	return <-vm.stoppedCh
}

// StartIORelay starts goroutines that relay between the VM serial console
// and the provided reader/writer (typically os.Stdin and os.Stdout).
func (vm *VM) StartIORelay(input io.Reader, output io.Writer) {
	go io.Copy(output, vm.stdoutReader)
	go io.Copy(vm.stdinWriter, input)
}

func (vm *VM) Close() {
	for i := range vm.vcpuFds {
		unix.Munmap(vm.vcpuRun[i])
		if vm.vcpuFds[i] >= 0 {
			unix.Close(vm.vcpuFds[i])
			vm.vcpuFds[i] = -1
		}
	}
	unix.Munmap(vm.memory)
	unix.Close(vm.vmFd)
	unix.Close(vm.kvmFd)
	vm.stdinReader.Close()
	vm.stdinWriter.Close()
	vm.stdoutReader.Close()
	vm.stdoutWriter.Close()
	for _, blk := range vm.blkDevs {
		blk.Close()
	}
	if vm.vsockDev != nil {
		vm.vsockDev.Close()
	}
	if vm.tap != nil {
		vm.tap.Close()
	}
}

func alignUp(val, align uint64) uint64 {
	return (val + align - 1) &^ (align - 1)
}

// setupGSIRouting configures KVM_SET_GSI_ROUTING so that IRQFD-triggered
// interrupts are routed to the in-kernel vGIC. On ARM64, the generic
// kvm_irq_map_gsi uses kvm->irq_routing (populated by this ioctl).
// Without it, eventfd writes from IRQFD are silently dropped.
func setupGSIRouting(vmFd int, devs []*virtioMMIODev) error {
	// Build routing entries for each device's SPI.
	// GSI = irqNum + 32 (SPI number → GIC IRQ number).
	type routingEntry struct {
		GSI     uint32
		Type    uint32 // KVM_IRQ_ROUTING_IRQCHIP = 1
		Flags   uint32
		Pad     uint32
		Irqchip uint32 // irqchip index (0 = GIC)
		Pin     uint32 // GIC interrupt ID = GSI
		UnionPad [24]byte // remaining union padding (total union = 32 bytes)
	}

	n := len(devs)
	// struct kvm_irq_routing: nr(4) + flags(4) + entries[]
	const entrySize = 48 // sizeof(struct kvm_irq_routing_entry)
	buf := make([]byte, 8+n*entrySize)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(n))
	binary.LittleEndian.PutUint32(buf[4:8], 0) // flags

	for i, dev := range devs {
		// The vgic_irqfd_set_irq handler adds VGIC_NR_PRIVATE_IRQS (32) to
		// the pin internally, so both GSI and pin should be the SPI number
		// (not the full GIC IRQ number). See vgic-irqfd.c:22.
		spiNum := dev.irqNum // SPI number (matches DTB interrupt cell)
		off := 8 + i*entrySize
		binary.LittleEndian.PutUint32(buf[off+0:off+4], spiNum)  // gsi = SPI number
		binary.LittleEndian.PutUint32(buf[off+4:off+8], 1)       // type = KVM_IRQ_ROUTING_IRQCHIP
		binary.LittleEndian.PutUint32(buf[off+16:off+20], 0)     // irqchip = 0 (GIC)
		binary.LittleEndian.PutUint32(buf[off+20:off+24], spiNum) // pin = SPI number
	}

	_, err := ioctlPtr(vmFd, kvmSetGSIRouting, unsafe.Pointer(&buf[0]))
	return err
}
