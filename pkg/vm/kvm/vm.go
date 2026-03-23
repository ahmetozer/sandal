//go:build linux

package kvm

import (
	"fmt"
	"io"
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
	vcpuFds []int
	vcpuRun [][]byte // mmap'd kvm_run regions per vCPU

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
	virtioDevs []*virtioMMIODev
	consoleDev *VirtioConsoleDevice
	netDevs    []*VirtioNetDevice
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
	{
		consoleDev = NewVirtioConsoleDevice(stdinReader, stdoutWriter)
		baseAddr := uint64(0x0a000000) + uint64(devIdx)*virtioMMIORegionSize
		irqNum := uint32(16 + devIdx)
		vDev := newVirtioMMIODev(baseAddr, irqNum, int(vmFd), mem, consoleDev)
		virtioDevs = append(virtioDevs, vDev)
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

	// Virtio-net device backed by TAP interface.
	tapName := fmt.Sprintf("sandal%d", os.Getpid()%10000)
	tap, err := createTAP(tapName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to create TAP device: %v (networking disabled)\n", err)
	} else {
		mac := [6]byte{0x52, 0x54, 0x00, byte(os.Getpid() >> 8), byte(os.Getpid()), 0x01}
		netDev := NewVirtioNetDevice(tap, mac)
		baseAddr := uint64(0x0a000000) + uint64(devIdx)*virtioMMIORegionSize
		irqNum := uint32(16 + devIdx)
		vDev := newVirtioMMIODev(baseAddr, irqNum, int(vmFd), mem, netDev)
		virtioDevs = append(virtioDevs, vDev)
		netDevs = append(netDevs, netDev)
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

	return &VM{
		Name:         name,
		Config:       cfg,
		kvmFd:        kvmFd,
		vmFd:         int(vmFd),
		vcpuFds:      vcpuFds,
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
		virtioDevs:   virtioDevs,
		consoleDev:   consoleDev,
		netDevs:      netDevs,
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

	// Start RX loops for virtio devices.
	for _, vd := range vm.virtioDevs {
		switch dev := vd.device.(type) {
		case *VirtioConsoleDevice:
			dev.StartRX(vd)
		case *VirtioNetDevice:
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
			if err == unix.EINTR || err == unix.EAGAIN {
				select {
				case <-vm.stopCh:
					return
				default:
					continue
				}
			}
			fmt.Fprintf(os.Stderr, "KVM_RUN[%d]: %v\n", cpuID, err)
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
			return
		case kvmExitShutdown:
			return
		case kvmExitSystemEvent:
			evType := *(*uint32)(unsafe.Pointer(&run[kvmRunExitUnionOffset]))
			switch evType {
			case kvmSystemEventShutdown:
				return
			case kvmSystemEventReset:
				fmt.Fprintf(os.Stderr, "vCPU %d: guest requested reset\n", cpuID)
				return
			case kvmSystemEventCrash:
				fmt.Fprintf(os.Stderr, "vCPU %d: guest crashed\n", cpuID)
				return
			}
		case kvmExitFailEntry:
			fmt.Fprintf(os.Stderr, "vCPU %d: fail entry\n", cpuID)
			return
		case kvmExitInternalErr:
			suberror := *(*uint32)(unsafe.Pointer(&run[kvmRunExitUnionOffset]))
			fmt.Fprintf(os.Stderr, "vCPU %d: KVM internal error suberror=%d\n", cpuID, suberror)
			return
		case kvmExitIntr:
			// Interrupted by signal, continue.
			continue
		default:
			fmt.Fprintf(os.Stderr, "vCPU %d: unhandled exit reason %d\n", cpuID, exitReason)
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
		unix.Close(vm.vcpuFds[i])
	}
	unix.Munmap(vm.memory)
	unix.Close(vm.vmFd)
	unix.Close(vm.kvmFd)
	vm.stdinReader.Close()
	vm.stdinWriter.Close()
	vm.stdoutReader.Close()
	vm.stdoutWriter.Close()
	if vm.tap != nil {
		vm.tap.Close()
	}
}

func alignUp(val, align uint64) uint64 {
	return (val + align - 1) &^ (align - 1)
}
