//go:build linux

package kvm

import (
	"fmt"
	"io"
	"os"
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
}

func NewVM(name string, cfg vmconfig.VMConfig) (*VM, error) {
	kvmFd, err := openKVM()
	if err != nil {
		return nil, err
	}

	// Create VM
	vmFd, err := ioctl(kvmFd, kvmCreateVM, 0)
	if err != nil {
		unix.Close(kvmFd)
		return nil, fmt.Errorf("KVM_CREATE_VM: %w", err)
	}

	// Platform-specific VM setup (IRQ chip, etc.)
	if err := setupVM(int(vmFd)); err != nil {
		unix.Close(int(vmFd))
		unix.Close(kvmFd)
		return nil, fmt.Errorf("VM setup: %w", err)
	}

	// Allocate guest memory
	mem, err := allocateGuestMemory(cfg.MemoryBytes)
	if err != nil {
		unix.Close(int(vmFd))
		unix.Close(kvmFd)
		return nil, err
	}

	// Map guest memory into VM
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

	// Load kernel into guest memory
	kernelSize, err := loadFileIntoMemory(mem, kernelLoadOffset, cfg.KernelPath)
	if err != nil {
		unix.Munmap(mem)
		unix.Close(int(vmFd))
		unix.Close(kvmFd)
		return nil, fmt.Errorf("loading kernel: %w", err)
	}

	// Load initrd if provided
	var initrdAddr, initrdSize uint64
	if cfg.InitrdPath != "" {
		// Place initrd after kernel with alignment
		initrdAddr = alignUp(kernelLoadOffset+kernelSize, 0x1000)
		initrdSize, err = loadFileIntoMemory(mem, initrdAddr, cfg.InitrdPath)
		if err != nil {
			unix.Munmap(mem)
			unix.Close(int(vmFd))
			unix.Close(kvmFd)
			return nil, fmt.Errorf("loading initrd: %w", err)
		}
	}

	// Create serial console pipes
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

	u := newUART(stdinReader, stdoutWriter)

	// Get vCPU mmap size
	vcpuMmapSize, err := getVCPUMmapSize(kvmFd)
	if err != nil {
		unix.Munmap(mem)
		unix.Close(int(vmFd))
		unix.Close(kvmFd)
		return nil, err
	}

	// Create vCPUs
	vcpuFds := make([]int, cfg.CPUCount)
	vcpuRun := make([][]byte, cfg.CPUCount)
	for i := uint(0); i < cfg.CPUCount; i++ {
		vcpuFd, err := ioctl(int(vmFd), kvmCreateVCPU, uintptr(i))
		if err != nil {
			// Cleanup already created vCPUs
			for j := uint(0); j < i; j++ {
				unix.Close(vcpuFds[j])
			}
			unix.Munmap(mem)
			unix.Close(int(vmFd))
			unix.Close(kvmFd)
			return nil, fmt.Errorf("KVM_CREATE_VCPU[%d]: %w", i, err)
		}
		vcpuFds[i] = int(vcpuFd)

		// mmap the kvm_run struct for this vCPU
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

	// Create VirtioFS devices for each mount
	var virtioDevs []*virtioMMIODev
	for i, mount := range cfg.Mounts {
		fsDev := NewVirtioFSDevice(mount.Tag, mount.HostPath, mount.ReadOnly)
		// Each virtio-mmio device gets its own MMIO region and SPI IRQ
		// Base addresses start at 0x0a000000, each 0x200 apart
		// IRQs start at SPI 16 (GIC IRQ 48)
		baseAddr := uint64(0x0a000000) + uint64(i)*virtioMMIORegionSize
		irqNum := uint32(48 + i) // SPI 16+i = GIC IRQ 48+i
		vDev := newVirtioMMIODev(baseAddr, irqNum, int(vmFd), mem, fsDev)
		virtioDevs = append(virtioDevs, vDev)
	}

	// Initialize vCPU registers (architecture-specific)
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

	vm.mu.Lock()
	vm.state = VMStateRunning
	vm.mu.Unlock()

	go func() {
		wg.Wait()
		vm.mu.Lock()
		vm.state = VMStateStopped
		vm.mu.Unlock()
		vm.stoppedCh <- nil
	}()

	return nil
}

func (vm *VM) runVCPU(cpuID int) {
	fd := vm.vcpuFds[cpuID]
	run := vm.vcpuRun[cpuID]
	for {
		select {
		case <-vm.stopCh:
			return
		default:
		}

		_, err := ioctl(fd, kvmRun, 0)
		if err != nil {
			// EINTR is normal when we send signals to stop
			if err == unix.EINTR {
				select {
				case <-vm.stopCh:
					return
				default:
					continue
				}
			}
			// EAGAIN can happen, retry
			if err == unix.EAGAIN {
				continue
			}
			fmt.Fprintf(os.Stderr, "KVM_RUN[%d]: %v\n", cpuID, err)
			return
		}

		exitReason := *(*uint32)(unsafe.Pointer(&run[8])) // offset of exit_reason in kvm_run

		switch exitReason {
		case kvmExitIO:
			vm.handleExitIO(run)
		case kvmExitMMIO:
			vm.handleExitMMIO(run)
		case kvmExitShutdown:
			return
		case kvmExitSystemEvent:
			return
		case kvmExitInternalErr:
			fmt.Fprintf(os.Stderr, "KVM internal error on vCPU %d\n", cpuID)
			return
		default:
			fmt.Fprintf(os.Stderr, "Unhandled KVM exit reason %d on vCPU %d\n", exitReason, cpuID)
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

	// Try virtio devices first
	for _, vdev := range vm.virtioDevs {
		if vdev.HandleMMIO(exitMMIO.PhysAddr, exitMMIO.Len, exitMMIO.IsWrite, exitMMIO.Data[:]) {
			return
		}
	}

	// Fall back to UART
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
	// Guest stdout -> host output
	go io.Copy(output, vm.stdoutReader)
	// Host input -> guest stdin
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
}

func alignUp(val, align uint64) uint64 {
	return (val + align - 1) &^ (align - 1)
}
