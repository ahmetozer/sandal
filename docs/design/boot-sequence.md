# Boot Sequence

This document describes the end-to-end flow from CLI invocation to user workload execution, covering both direct container and VM-isolated paths.

## Entry Point Dispatch

**File**: `main.go`, `main_linux.go`

```
main()
  -> platformMain()     [main_linux.go]
       |
       +-- PID == 1 && SANDAL_VM_ARGS set?
       |     YES -> guest.VMInit()   # VM guest init (Phase 6)
       |            cmd.Main()       # Re-dispatch CLI in VM
       |
       +-- SANDAL_CHILD env set?
       |     YES -> guest.ContainerInitProc()  # Container child (Phase 5)
       |
       +-- DEFAULT -> cmd.Main()     # Host CLI (Phase 1)
```

The same binary serves three roles depending on context:
1. **Host CLI**: Normal invocation on the host system
2. **VM Init**: Running as PID 1 inside a KVM guest
3. **Container Init**: Running as the forked child process inside isolated namespaces

## Phase 1: CLI Parsing

**File**: `pkg/cmd/main.go`

`cmd.Main()` dispatches based on `os.Args[1]`:

| Subcommand | Handler | Description |
|-----------|---------|-------------|
| `run` | `run.Run()` | Create and start a container |
| `ps` | `cmdPs()` | List running containers |
| `kill` | `cmdKill()` | Send signal to container |
| `exec` | `cmdExec()` | Execute command in running container |
| `daemon` | `cmdDaemon()` | Start background daemon |
| `vm` | `cmdVM()` | VM management (macOS) |
| ... | ... | Other management commands |

## Phase 2: Run Command Router

**File**: `pkg/cmd/run/run_linux.go`

```
run.Run(args)
  |
  +-- Has "--vm" flag AND not already in VM?
  |     YES -> runInKVM(args)     # Phase 3
  |
  +-- NO -> runContainer(args)    # Phase 4
```

Detection of "already in VM" uses `env.IsVM()` which checks the `SANDAL_VM` environment variable.

## Phase 3: KVM VM Launch

**File**: `pkg/cmd/run/vm_linux.go`

```
runInKVM(args)
  |
  +-- 1. extractFlag("--vm") -> remove --vm from args
  |
  +-- 2. scanMountPaths(args) -> collect -v mount paths for VirtioFS
  |      Each -v /host/path:/guest/path becomes a VirtioFS mount
  |
  +-- 3. squash.PullFromArgs(args) -> pre-pull OCI images to squashfs cache
  |      Downloads image layers, flattens to single squashfs file
  |      Replaces image reference in args with cache path
  |
  +-- 4. kernel.EnsureKernel() -> download Linux kernel if not cached
  |      Source: Alpine Linux APK repository (linux-virt package)
  |      Handles ZBOOT extraction (ARM64 EFI compressed format)
  |      Cache: ~/.sandal-vm/kernel/<arch>/
  |
  +-- 5. buildVirtioFSMounts() -> create mount configs
  |      Each mount gets tag: fs-0, fs-1, fs-2, ...
  |      Always includes: sandal library dir, image cache dir
  |
  +-- 6. marshalVMArgs() -> JSON-encode original CLI args
  |
  +-- 7. sandalnet.CreateDefaultBridge() -> ensure sandal0 bridge exists
  |
  +-- 8. sandalnet.IPRequest() -> allocate IP from bridge subnet
  |
  +-- 9. buildKernelCmdLine() -> construct kernel parameters
  |      SANDAL_VM_ARGS=<base64(json_args)>
  |      SANDAL_VM_NET=<base64(json_network_config)>
  |      SANDAL_VM_MOUNTS=<comma_separated_mount_specs>
  |      SANDAL_VM=kvm
  |      console=ttyAMA0 (ARM64) or console=ttyS0 (x86)
  |      loglevel=0
  |
  +-- 10. resolveVMBinary() -> get sandal binary path for /init
  |
  +-- 11. kernel.CreateFromBinary(binary, baseInitrd) -> build initrd
  |       Wraps sandal binary into CPIO archive as /sandal-init
  |       Includes preinit (ARM64): tiny ELF that mounts /proc, /dev
  |       Appends to base initrd (kernel modules)
  |
  +-- 12. vmconfig.SaveConfig(name, cfg) -> persist to disk
  |       Path: ~/.sandal-vm/machines/<name>/config.json
  |
  +-- 13. kvm.Boot(name, cfg) -> launch VM (Phase 3a)
```

### Phase 3a: KVM VM Creation

**File**: `pkg/vm/kvm/boot.go`, `pkg/vm/kvm/vm.go`

```
kvm.Boot(name, cfg)
  |
  +-- terminal.SetRaw() -> put host terminal in raw mode
  |
  +-- NewVM(name, cfg)
  |   |
  |   +-- openKVM() -> fd = open("/dev/kvm")
  |   |   verify KVM_GET_API_VERSION == 12
  |   |
  |   +-- ioctl(kvmFd, KVM_CREATE_VM, 0) -> vmFd
  |   |
  |   +-- setupVM(vmFd) -> platform-specific (x86: TSS, IRQCHIP)
  |   |
  |   +-- mmap(cfg.MemoryBytes, MAP_PRIVATE|MAP_ANONYMOUS|MAP_NORESERVE) -> mem[]
  |   |   Allocates guest RAM with overcommit (no physical reservation)
  |   |
  |   +-- ioctl(vmFd, KVM_SET_USER_MEMORY_REGION, {slot:0, gpa:guestMemBase, size, hva})
  |   |   Registers guest memory with KVM
  |   |
  |   +-- loadFileIntoMemory(mem, kernelLoadOffset, cfg.KernelPath)
  |   |   ARM64: kernel at offset 0x0 (guestMemBase + 0)
  |   |   x86: kernel at offset 0x100000 (1MB)
  |   |
  |   +-- loadFileIntoMemory(mem, initrdOffset, cfg.InitrdPath)
  |   |   ARM64: initrd at 50MB offset
  |   |   x86: initrd after kernel
  |   |
  |   +-- Create stdin/stdout pipe pair for serial console
  |   +-- newUART(irqLine, stdinReader, stdoutWriter)
  |   |
  |   +-- getVCPUMmapSize(kvmFd) -> size of kvm_run struct
  |   |
  |   +-- For each CPU i in 0..CPUCount-1:
  |   |     ioctl(vmFd, KVM_CREATE_VCPU, i) -> vcpuFd
  |   |     mmap(vcpuFd, vcpuMmapSize) -> kvm_run shared memory region
  |   |
  |   +-- Create virtio devices (in order):
  |   |     [0] VirtioConsoleDevice (device ID 3)
  |   |     [1] VirtioBlkDevice for disk (device ID 2) if DiskPath set
  |   |     [2] VirtioBlkDevice for ISO (device ID 2, read-only) if ISOPath set
  |   |     [3..N] VirtioFSDevice for each mount (device ID 26)
  |   |     [N+1] VirtioNetDevice (device ID 1) with TAP backend
  |   |
  |   +-- initVCPUs(vmFd, vcpuFds, mem, cfg)
  |   |     ARM64: See "ARM64 vCPU Init" below
  |   |     x86: See "x86 vCPU Init" below
  |   |
  |   +-- setupGSIRouting(vmFd, devices) -> KVM_SET_GSI_ROUTING
  |   |     Maps each device's GSI to GIC SPI interrupt
  |   |
  |   +-- For each virtio device:
  |         setupIRQFD(vmFd, dev) -> KVM_IRQFD with eventfd
  |         Enables userspace interrupt injection without ioctl per IRQ
  |
  +-- vm.StartIORelay(os.Stdin, os.Stdout)
  |     Goroutines: host stdin -> VM stdin pipe, VM stdout pipe -> host stdout
  |
  +-- signal.Notify(SIGINT, SIGTERM) -> vm.Stop()
  |
  +-- vm.Start()
  |   |
  |   +-- For each vCPU: go runVCPU(i)
  |   +-- consoleDev.StartRX() -> goroutine reads stdin pipe, injects to RX queue
  |   +-- For each netDev: netDev.StartRX() -> goroutine reads TAP, injects to RX queue
  |
  +-- vm.WaitUntilStopped() -> blocks until all vCPUs exit
```

### ARM64 vCPU Initialization

**File**: `pkg/vm/kvm/vcpu_arm64.go`

```
initVCPUs() [ARM64]
  |
  +-- For each vCPU:
  |     ioctl(vmFd, KVM_ARM_PREFERRED_TARGET) -> get init struct
  |     ioctl(vcpuFd, KVM_ARM_VCPU_INIT, initStruct)
  |
  +-- CPU 0 (primary):
  |     Set PC = guestMemBase + kernelLoadOffset (kernel entry)
  |     Set X0 = guestMemBase + dtbOffset (DTB address)
  |     Set PSTATE = 0x3c5 (EL1h, DAIF masked)
  |
  +-- CPUs 1+ (secondary):
  |     Set PSCI power-off state (activated by primary via HVC)
  |
  +-- buildDTB(cfg, mem, deviceList) -> create flattened device tree
  |     Memory node, CPU nodes, GIC node, UART node
  |     Virtio-MMIO device nodes with IRQ assignments
  |     Chosen node: bootargs, stdout-path, initrd addresses
  |
  +-- createGIC(vmFd)
        Try GICv3 first (distributor + redistributor)
        Fallback to GICv2 (distributor + CPU interface)
        GIC dist @ 0x08000000, redist @ 0x080A0000
```

### x86-64 vCPU Initialization

**File**: `pkg/vm/kvm/vcpu_amd64.go`

```
initVCPUs() [x86-64]
  |
  +-- Setup page tables in guest memory:
  |     PML4 @ 0x1000, PDPT @ 0x2000, PD @ 0x3000
  |     Identity maps first 4GB with 2MB pages
  |
  +-- Setup GDT @ 0x500:
  |     Null, code64 (L=1), data64 segments
  |
  +-- Setup boot parameters (zero page) @ 0x10000:
  |     Linux boot protocol v2.14
  |     E820 memory map
  |     Kernel cmdline @ 0x20000
  |     Initrd address and size
  |
  +-- Configure special registers:
  |     CR3 = 0x1000 (PML4)
  |     CR4 = PAE
  |     CR0 = PG | PE | WP
  |     EFER = LME | LMA (long mode)
  |
  +-- Set initial register state:
        RIP = 0x100000 (kernel entry at 1MB)
        RSI = 0x10000 (boot params)
        CS = 64-bit code segment
        DS/ES/SS = data segment
```

## Phase 4: Container Creation (Direct Path)

**File**: `pkg/cmd/run/container.go`, `pkg/container/host/crun.go`

```
runContainer(args)
  |
  +-- Parse flags: image, -v volumes, -d daemon, -t tty, --ns-*, --cap-*, etc.
  +-- Generate container ID (base-62 timestamp + random)
  +-- Resolve rootfs directory and change directory
  +-- Parse namespace configuration (defaults: new mnt, pid, ipc, uts, net)
  +-- Mount rootfs (overlayfs with image as lower, changedir as upper)
  +-- controller.SetContainer(cfg) -> save config JSON
  +-- host.Run(cfg)
        |
        +-- Allocate PTY (foreground) or FIFO (daemon)
        +-- Create cgroup: /sys/fs/cgroup/sandal/<name>/
        +-- Set resource limits (memory.max, cpu.max)
        +-- Get clone flags from namespace config
        +-- SysProcAttr{Cloneflags: CLONE_NEWNS|CLONE_NEWPID|...}
        +-- Fork child process with SANDAL_CHILD=<config_name>
        +-- Parent: signal forwarding, PTY relay, wait for exit
        +-- Child: ContainerInitProc() (Phase 5)
```

## Phase 5: Container Init Process

**File**: `pkg/container/guest/init.go`

```
ContainerInitProc()
  |
  +-- Load container config from SANDAL_CHILD env var
  |
  +-- Set hostname
  |
  +-- Configure network interfaces:
  |     For each network spec:
  |       Create/join veth pair
  |       Set master bridge (sandal0)
  |       Assign IP (static or DHCP)
  |       Add routes
  |
  +-- Mount volumes (-v binds)
  +-- Mount /proc (with cpuinfo/meminfo overrides for cgroup limits)
  +-- Mount /sys, /dev, /dev/pts, /dev/shm, /tmp
  +-- Create device nodes (null, zero, full, random, urandom, tty)
  |
  +-- Configure DNS (/etc/resolv.conf)
  +-- Set hostname (/etc/hostname)
  |
  +-- Pivot root to container rootfs
  |     mount("", "/", "", MS_PRIVATE|MS_REC, "")
  |     pivot_root(rootfs, oldroot)
  |     unmount(oldroot)
  |
  +-- Drop capabilities (unless --privileged)
  |     Keep: NET_BIND_SERVICE, SYS_CHROOT, SETGID, SETUID, ...
  |     Drop: SYS_ADMIN, SYS_PTRACE, NET_ADMIN (non-privileged)
  |
  +-- Switch user (if --user specified)
  |     setresgid(), setresuid() with PR_SET_KEEPCAPS
  |
  +-- Exec user command
        syscall.Exec(containerArgs[0], containerArgs, env)
```

## Phase 6: VM Guest Initialization

**File**: `pkg/vm/guest/init.go`

This runs when the sandal binary is PID 1 inside the KVM guest.

```
VMInit()
  |
  +-- importKernelCmdlineEnv()
  |     Parse /proc/cmdline for SANDAL_* parameters
  |     Base64-decode values, set as environment variables
  |
  +-- Mount essential filesystems:
  |     mount("proc", "/proc", "proc", 0, "")
  |     mount("devtmpfs", "/dev", "devtmpfs", 0, "")
  |     mount("sysfs", "/sys", "sysfs", 0, "")
  |     mount("devpts", "/dev/pts", "devpts", 0, "")
  |
  +-- Redirect stdin/stdout/stderr to /dev/console
  |
  +-- Load kernel modules (ordered, virtio_mmio MUST be first):
  |     Group 1 (virtio): virtio_mmio
  |     Group 2 (storage): fuse, virtiofs, overlay, loop, squashfs, ext4
  |     Group 3 (network): veth, bridge, tun, af_packet
  |     Group 4 (netfilter): nf_conntrack, nf_nat, iptable_nat, ip_vs, ...
  |     Group 5 (tunneling): vxlan, ip_gre, wireguard
  |
  +-- Setup new root:
  |     mount("tmpfs", "/newroot", "tmpfs", 0, "")
  |     Copy /init to /newroot/init
  |     Create dirs: /proc, /sys, /dev, /mnt, /var/lib/sandal, ...
  |     chroot("/newroot")
  |
  +-- Mount VirtioFS shares:
  |     Decode SANDAL_VM_MOUNTS: "fs-0=/host/data,fs-1=/home/user/.sandal-vm"
  |     For each mount:
  |       mkdir -p <mountpoint>
  |       mount(tag, mountpoint, "virtiofs", 0, "")
  |
  +-- Configure network:
  |     Decode SANDAL_VM_NET JSON: {addr, gateway, mac, mtu}
  |     ip addr add <addr> dev eth0
  |     ip link set eth0 up
  |     ip route add default via <gateway>
  |
  +-- Return to platformMain()
        cmd.Main() is called, dispatching the original CLI args
        from SANDAL_VM_ARGS (e.g., "run alpine:latest /bin/sh")
        This triggers Phase 4 (runContainer) inside the VM
```

## vCPU Run Loop

**File**: `pkg/vm/kvm/vm.go`

```
runVCPU(cpuIndex)
  |
  +-- Block SIGURG signal (prevents Go scheduler preemption during KVM_RUN)
  |     KVM_SET_SIGNAL_MASK with SIGURG blocked
  |
  +-- Loop:
        ioctl(vcpuFd, KVM_RUN, 0) -> blocks until VM exit
        |
        +-- Read exit_reason from mmap'd kvm_run struct
        |
        +-- Switch on exit_reason:
              |
              +-- kvmExitIO (2) [x86 only]:
              |     Port I/O access (UART at 0x3F8)
              |     Route to uart.handleIO()
              |
              +-- kvmExitMMIO (6):
              |     Memory-mapped I/O access
              |     Check address range:
              |       0x09000000-0x09000FFF -> uart.handleMMIO() [ARM64]
              |       0x0A000000+ -> virtio device MMIO handler
              |     Route to appropriate virtioMMIODev.handleMMIO()
              |
              +-- kvmExitHLT (5) [x86]:
              |     Guest halted, check if should continue
              |
              +-- kvmExitShutdown (8):
              |     Guest requested shutdown
              |     Signal VM stop
              |
              +-- kvmExitSystemEvent (24):
              |     PSCI SYSTEM_OFF or SYSTEM_RESET
              |     Signal VM stop
              |
              +-- kvmExitIntr (10):
                    Interrupted by signal, continue loop
```
