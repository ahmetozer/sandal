# KVM Hypervisor

The KVM hypervisor (`pkg/vm/kvm/`) implements a lightweight virtual machine monitor using Linux KVM. It supports ARM64 and x86-64 architectures with virtio MMIO device emulation, all in pure Go with no CGO.

## Architecture

```
+---------------------------------------------------+
|  Host Userspace (sandal binary)                   |
|                                                    |
|  +-------+  +----------+  +-------------------+   |
|  | vCPU  |  | UART     |  | Virtio MMIO       |   |
|  | Mgr   |  | Console  |  | Transport         |   |
|  +---+---+  +----+-----+  +---+---+---+---+---+   |
|      |           |             |   |   |   |       |
|      |     +-----+------+     |   |   |   |       |
|      |     | stdin/out  |     |   |   |   |       |
|      |     +------------+     |   |   |   |       |
|      |                  +-----+   |   |   +---+   |
|      |                  | Console |   |       |   |
|      |                  +---------+   |   +---+   |
|      |                       +--------+   | Net|  |
|      |                       | Block  |   +---+   |
|      |                       +--------+   | FS |  |
|      |                                    +---+   |
+------+--------------------------------------------+
       |                     Guest Memory (mmap)
+------+--------------------------------------------+
|  /dev/kvm  (KVM kernel module)                    |
+---------------------------------------------------+
|  Hardware (VT-x / ARM VHE)                        |
+---------------------------------------------------+
```

## KVM API Layer

**File**: `pkg/vm/kvm/kvm.go`

### System-Level ioctls (on `/dev/kvm` fd)

| ioctl | Constant | Purpose |
|-------|----------|---------|
| `KVM_GET_API_VERSION` | `0xAE00` | Returns 12 (expected version) |
| `KVM_CREATE_VM` | `0xAE01` | Creates VM instance, returns vmFd |
| `KVM_CHECK_EXTENSION` | `0xAE03` | Query supported capabilities |
| `KVM_GET_VCPU_MMAP_SIZE` | `0xAE04` | Size of per-vCPU shared memory |

### VM-Level ioctls (on vmFd)

| ioctl | Constant | Purpose |
|-------|----------|---------|
| `KVM_CREATE_VCPU` | `0xAE41` | Create virtual CPU, returns vcpuFd |
| `KVM_SET_USER_MEMORY_REGION` | `0x4020AE46` | Register guest physical memory |
| `KVM_CREATE_DEVICE` | `0xC00CAEE0` | Create in-kernel device (GIC) |
| `KVM_IRQ_LINE` | `0x4008AE61` | Assert/deassert IRQ line (ARM64 UART) |
| `KVM_SET_GSI_ROUTING` | `0x4008AE6A` | Configure IRQ routing table |
| `KVM_IRQFD` | `0x4020AE76` | Register eventfd for interrupt injection |
| `KVM_CREATE_IRQCHIP` | `0xAE60` | Create in-kernel IRQ chip (x86) |
| `KVM_SET_TSS_ADDR` | `0xAE47` | Set TSS address (x86) |

### vCPU-Level ioctls (on vcpuFd)

| ioctl | Constant | Purpose |
|-------|----------|---------|
| `KVM_RUN` | `0xAE80` | Execute vCPU until exit |
| `KVM_ARM_VCPU_INIT` | `0x4020AEAE` | Initialize ARM vCPU |
| `KVM_GET_ONE_REG` | `0x4010AEAB` | Read single ARM register |
| `KVM_SET_ONE_REG` | `0x4010AEAC` | Write single ARM register |
| `KVM_GET_REGS` | `0x8090AE81` | Read all registers (x86) |
| `KVM_SET_REGS` | `0x4090AE82` | Write all registers (x86) |
| `KVM_SET_SREGS` | `0x4138AE84` | Write special registers (x86) |
| `KVM_SET_SIGNAL_MASK` | `0x4004AE8B` | Block signals during KVM_RUN |

### Wrapper Functions

```go
func ioctl(fd int, request, arg uintptr) (uintptr, error)
func ioctlPtr(fd int, request uintptr, arg unsafe.Pointer) (uintptr, error)
```

Both use `unix.Syscall(unix.SYS_IOCTL, ...)` directly.

## VM Structure

**File**: `pkg/vm/kvm/vm.go`

```go
type VM struct {
    name      string
    kvmFd     int            // /dev/kvm file descriptor
    vmFd      int            // VM instance fd (from KVM_CREATE_VM)
    vcpuFds   []int          // Per-vCPU fds (from KVM_CREATE_VCPU)
    vcpuRun   [][]byte       // Per-vCPU mmap'd kvm_run regions
    memory    []byte         // Guest RAM (mmap'd host buffer)
    memSize   uint64         // Guest RAM size in bytes

    // Devices
    uart       *uart                // Serial console (PL011 or 16550)
    consoleDev *VirtioConsoleDevice // Virtio console
    netDevs    []*VirtioNetDevice   // Virtio network devices
    blkDevs    []*VirtioBlkDevice   // Virtio block devices
    virtioDevs []*virtioMMIODev     // All virtio devices (MMIO transport)
    tap        *tapDevice           // TAP network interface

    // Console I/O pipes
    stdinReader  *os.File
    stdinWriter  *os.File
    stdoutReader *os.File
    stdoutWriter *os.File

    // State
    state     VMState
    stateMu   sync.Mutex
    stopCh    chan struct{}
    stoppedCh chan struct{}
    wg        sync.WaitGroup
}
```

### VM States

```go
const (
    VMStateStopped  VMState = 0
    VMStateRunning  VMState = 1
    VMStatePaused   VMState = 2
    VMStateError    VMState = 3
    VMStateStarting VMState = 4
    VMStateStopping VMState = 7
)
```

## Memory Model

### Allocation

```go
// vm.go - NewVM()
memory, _ := unix.Mmap(-1, 0, int(cfg.MemoryBytes),
    unix.PROT_READ|unix.PROT_WRITE,
    unix.MAP_PRIVATE|unix.MAP_ANONYMOUS|unix.MAP_NORESERVE)
```

`MAP_NORESERVE` allows physical memory overcommit - the host kernel doesn't reserve swap space for the full allocation. Memory pages are allocated on demand (page fault).

### Memory Registration

```go
region := kvmUserspaceMemoryRegion{
    Slot:          0,
    GuestPhysAddr: guestMemBase,  // 0x40000000 (ARM64) or 0x0 (x86)
    MemorySize:    cfg.MemoryBytes,
    UserspaceAddr: uint64(uintptr(unsafe.Pointer(&memory[0]))),
}
ioctl(vmFd, KVM_SET_USER_MEMORY_REGION, uintptr(unsafe.Pointer(&region)))
```

### Guest Physical Address Layout

#### ARM64

```
0x00000000 - 0x07FFFFFF    Unmapped (below GIC)
0x08000000 - 0x0800FFFF    GIC Distributor
0x08010000 - 0x0801FFFF    GIC CPU Interface (GICv2)
0x080A0000 - 0x08FFFFFF    GIC Redistributor (GICv3)
0x09000000 - 0x09000FFF    PL011 UART
0x0A000000 - 0x0A001FFF    Virtio MMIO devices (0x200 each)
0x40000000                  Guest RAM base (guestMemBase)
0x40000000 + 0x00000000    Kernel image load address
0x40000000 + 0x03000000    DTB (48MB offset)
0x40000000 + 0x03200000    Initrd (50MB offset)
```

#### x86-64

```
0x00000000                  Guest RAM base (guestMemBase = 0)
0x00000500                  GDT
0x00001000                  PML4 page table
0x00002000                  PDPT page table
0x00003000                  PD page table
0x00010000                  Boot parameters (zero page)
0x00020000                  Kernel command line
0x00100000                  Kernel image load address (1MB)
0x0A000000+                 Virtio MMIO devices (0x200 each)
```

### Guest Memory Access Helpers

```go
func (vm *VM) guestSlice(gpa uint64, size uint64) []byte {
    offset := gpa - guestMemBase
    return vm.memory[offset : offset+size]
}

func (vm *VM) readGuestU16(gpa uint64) uint16
func (vm *VM) readGuestU32(gpa uint64) uint32
func (vm *VM) readGuestU64(gpa uint64) uint64
func (vm *VM) writeGuestU16(gpa uint64, val uint16)
func (vm *VM) writeGuestU32(gpa uint64, val uint32)
```

These translate Guest Physical Addresses (GPA) to Host Virtual Addresses (HVA) by offsetting into the mmap'd memory buffer.

## vCPU Lifecycle

### Creation

```go
for i := 0; i < cfg.CPUCount; i++ {
    vcpuFd, _ := ioctl(vmFd, KVM_CREATE_VCPU, uintptr(i))
    vcpuRun, _ := unix.Mmap(vcpuFd, 0, vcpuMmapSize, PROT_READ|PROT_WRITE, MAP_SHARED)
    vm.vcpuFds[i] = int(vcpuFd)
    vm.vcpuRun[i] = vcpuRun
}
```

The mmap'd `kvm_run` region is shared between kernel and userspace. After `KVM_RUN` returns, the exit reason and associated data are read from this region.

### Run Loop

**File**: `pkg/vm/kvm/vm.go` - `runVCPU()`

```go
func (vm *VM) runVCPU(idx int) {
    // Block SIGURG to prevent Go scheduler from interrupting KVM_RUN
    // during WFI (Wait For Interrupt) which would cause spurious wakeups
    blockSIGURG(vm.vcpuFds[idx])

    for {
        select {
        case <-vm.stopCh:
            return
        default:
        }

        _, err := ioctl(vm.vcpuFds[idx], KVM_RUN, 0)
        if err == syscall.EINTR {
            continue  // Interrupted by signal, retry
        }

        run := vm.vcpuRun[idx]
        exitReason := binary.LittleEndian.Uint32(run[8:12])

        switch exitReason {
        case kvmExitIO:       // x86 port I/O
            vm.handleExitIO(run)
        case kvmExitMMIO:     // Memory-mapped I/O
            vm.handleExitMMIO(run)
        case kvmExitHLT:      // Guest halted
            // Check stop channel
        case kvmExitShutdown: // Guest shutdown
            vm.Stop()
            return
        case kvmExitSystemEvent: // PSCI shutdown/reboot
            vm.Stop()
            return
        case kvmExitIntr:     // Interrupted by signal
            continue
        }
    }
}
```

### MMIO Exit Handling

```go
func (vm *VM) handleExitMMIO(run []byte) {
    phys := binary.LittleEndian.Uint64(run[mmioPhysOffset:])
    data := run[mmioDataOffset : mmioDataOffset+8]
    size := binary.LittleEndian.Uint32(run[mmioLenOffset:])
    isWrite := run[mmioIsWriteOffset] != 0

    // Route to UART or virtio device based on address
    if phys >= 0x09000000 && phys < 0x09001000 {
        vm.uart.handleMMIO(phys, data, size, isWrite)
    } else {
        for _, dev := range vm.virtioDevs {
            if phys >= dev.baseAddr && phys < dev.baseAddr+0x200 {
                dev.handleMMIO(phys-dev.baseAddr, data, size, isWrite)
                break
            }
        }
    }
}
```

## Interrupt Delivery

### Mechanism

Sandal uses **eventfd-based interrupt injection** (KVM_IRQFD) for virtio devices, which is more efficient than calling KVM_IRQ_LINE per interrupt:

```go
func setupIRQFD(vmFd int, dev *virtioMMIODev) {
    // Create eventfd for IRQ injection
    efd, _ := unix.Eventfd(0, unix.EFD_CLOEXEC)
    // Create resample eventfd for level-triggered IRQ acknowledgment
    resampleEfd, _ := unix.Eventfd(0, unix.EFD_CLOEXEC)

    irqfd := kvmIRQFD{
        Fd:    uint32(efd),
        GSI:   dev.irqNum,
        Flags: kvmIRQFDFlagResample,
        ResampleFd: uint32(resampleEfd),
    }
    ioctl(vmFd, KVM_IRQFD, uintptr(unsafe.Pointer(&irqfd)))

    dev.irqEfd = efd
    dev.resampleEfd = resampleEfd
}

func (dev *virtioMMIODev) injectIRQ() {
    buf := [8]byte{}
    binary.LittleEndian.PutUint64(buf[:], 1)
    unix.Write(dev.irqEfd, buf[:])  // Triggers interrupt in guest
}
```

### GSI Routing

**File**: `pkg/vm/kvm/vm.go`

GSI (Global System Interrupt) routing maps device interrupt numbers to the in-kernel interrupt controller:

```go
func setupGSIRouting(vmFd int, devs []*virtioMMIODev) {
    entries := make([]kvmIRQRoutingEntry, len(devs))
    for i, dev := range devs {
        entries[i] = kvmIRQRoutingEntry{
            GSI:  dev.irqNum,
            Type: KVM_IRQ_ROUTING_IRQCHIP,
            // ARM64: irqchip=0 (GIC), pin=irqNum
            // x86: irqchip=ioapic, pin=irqNum
        }
    }
    routing := kvmIRQRouting{Nr: uint32(len(entries)), Entries: entries}
    ioctl(vmFd, KVM_SET_GSI_ROUTING, uintptr(unsafe.Pointer(&routing)))
}
```

### UART Interrupt (ARM64)

The UART uses direct `KVM_IRQ_LINE` instead of eventfd, since it's a simple level-triggered interrupt:

```go
func (u *uart) setIRQ(level int) {
    irq := kvmIRQLevel{IRQ: u.irqNum, Level: uint32(level)}
    ioctl(u.vmFd, KVM_IRQ_LINE, uintptr(unsafe.Pointer(&irq)))
}
```

## ARM64 GIC (Generic Interrupt Controller)

**File**: `pkg/vm/kvm/vcpu_arm64.go`

```go
func createGIC(vmFd int) {
    // Try GICv3 first
    dev := kvmCreateDevice{Type: kvmDevTypeARMVGICv3}
    _, err := ioctl(vmFd, KVM_CREATE_DEVICE, ...)
    if err != nil {
        // Fallback to GICv2
        dev.Type = kvmDevTypeARMVGICv2
        ioctl(vmFd, KVM_CREATE_DEVICE, ...)
    }

    // Set distributor base address
    setDeviceAttr(devFd, KVM_DEV_ARM_VGIC_GRP_ADDR, addrTypeDist, gicDistBase)

    // GICv3: set redistributor address
    // GICv2: set CPU interface address
    setDeviceAttr(devFd, KVM_DEV_ARM_VGIC_GRP_ADDR, addrType, baseAddr)

    // Initialize GIC
    setDeviceAttr(devFd, KVM_DEV_ARM_VGIC_GRP_CTRL, 0, 0)
}
```

**GIC memory map**:
```
0x08000000    GIC Distributor (64KB)
0x08010000    GIC CPU Interface (64KB) - GICv2 only
0x080A0000    GIC Redistributor (per-CPU, 128KB each) - GICv3 only
```

## Device Tree Blob (DTB)

**File**: `pkg/vm/kvm/dtb.go` (ARM64 only)

The DTB is built programmatically in FDT (Flattened Device Tree) format:

```
/ {
    compatible = "linux,dummy-virt";
    #address-cells = <2>;
    #size-cells = <2>;
    interrupt-parent = <&gic>;

    chosen {
        bootargs = "<kernel cmdline>";
        stdout-path = "/pl011@9000000";
        linux,initrd-start = <initrd_addr>;
        linux,initrd-end = <initrd_addr + initrd_size>;
    };

    memory@40000000 {
        device_type = "memory";
        reg = <0x0 0x40000000 0x0 <mem_size>>;
    };

    cpus {
        cpu@0 { device_type = "cpu"; compatible = "arm,arm-v8"; enable-method = "psci"; };
        cpu@1 { ... };
    };

    psci {
        compatible = "arm,psci-0.2";
        method = "hvc";
    };

    intc@8000000 {  // GIC
        compatible = "arm,gic-v3" or "arm,cortex-a15-gic";
        interrupt-controller;
        reg = <dist_base dist_size redist_base redist_size>;
    };

    pl011@9000000 {  // UART
        compatible = "arm,pl011", "arm,primecell";
        reg = <0x0 0x09000000 0x0 0x1000>;
        interrupts = <GIC_SPI 1 IRQ_TYPE_LEVEL_HIGH>;
    };

    virtio_mmio@a000000 {  // Virtio device 0
        compatible = "virtio,mmio";
        reg = <0x0 0x0a000000 0x0 0x200>;
        interrupts = <GIC_SPI 16 IRQ_TYPE_EDGE_RISING>;
    };
    // ... more virtio devices at 0x200 intervals
};
```

## UART Emulation

**File**: `pkg/vm/kvm/uart.go`

### ARM64: PL011 UART at 0x09000000

| Offset | Register | Read | Write |
|--------|----------|------|-------|
| 0x000 | DR | Read received char | Write char to transmit |
| 0x018 | FR | Flags (TXFE, RXFE) | - |
| 0x038 | IMSC | Interrupt mask | Set interrupt mask |
| 0x03C | RIS | Raw interrupt status | - |
| 0x040 | MIS | Masked interrupt status | - |
| 0x044 | ICR | - | Clear interrupt |
| 0xFE0-0xFFC | ID regs | PL011 identification | - |

### x86-64: 16550 UART at I/O port 0x3F8

| Port | Register | Purpose |
|------|----------|---------|
| 0x3F8 | RBR/THR | Receive/Transmit data |
| 0x3F9 | IER | Interrupt enable |
| 0x3FD | LSR | Line status (TX ready, RX ready) |

### Interrupt Logic

```
Input arrives (host stdin -> pipe -> UART buffer)
  -> Set RX interrupt pending
  -> If RX interrupt enabled (IMSC bit 4):
       -> Assert IRQ line (KVM_IRQ_LINE level=1)
  -> Guest reads DR register
  -> Clear RX pending
  -> If no more pending interrupts:
       -> Deassert IRQ line (KVM_IRQ_LINE level=0)
```

## Signal Handling

The vCPU run loop blocks `SIGURG` using `KVM_SET_SIGNAL_MASK`:

```go
func blockSIGURG(vcpuFd int) {
    // SIGURG (signal 23) is used by Go runtime for goroutine preemption.
    // If not blocked, it causes spurious KVM_EXIT_INTR during guest WFI,
    // wasting CPU cycles.
    mask := kvmSignalMask{Len: 8}
    mask.Sigset[0] = 1 << (23 - 1)  // Block SIGURG
    ioctl(vcpuFd, KVM_SET_SIGNAL_MASK, uintptr(unsafe.Pointer(&mask)))
}
```
