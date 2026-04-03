# Architecture Overview

## What is Sandal?

Sandal is a lightweight, portable container runtime for Linux. It can run containers directly on the host using Linux namespaces, or inside lightweight KVM virtual machines for stronger isolation. The entire system compiles to a single static binary with no external dependencies (no containerd, no runc, no virtiofsd).

### Design Goals

1. **Single binary**: No daemon required for basic operation; optional daemon for persistent containers.
2. **Portable images**: Supports directories, raw disk images (IMG), SquashFS, and OCI container images.
3. **Field-deployable**: Designed for embedded systems, SD cards, Raspberry Pi. Works on ARM64 and x86-64.
4. **VM-optional isolation**: Containers can run directly on the host or inside a KVM VM with the same CLI.
5. **Built-in everything**: OCI image pulling, DHCP client, VirtioFS server, kernel download - all in one binary.

## Component Map

```
+------------------------------------------------------------------+
|                        sandal CLI                                 |
|  (pkg/cmd/)                                                      |
|  run | ps | kill | exec | attach | snapshot | export | daemon    |
+------+-----------------------------------------------------------+
       |
       v
+------+-----------------------------------------------------------+
|                     Run Command Router                            |
|  (pkg/sandal/)                                                   |
|                                                                   |
|  --vm flag?  ----YES----> RunInVM()      (VM path)               |
|              ----NO-----> RunContainer() (Direct path)            |
+------+------------------------+----------------------------------+
       |                        |
       v                        v
+------+---------+    +---------+----------------------------------+
| Container      |    | KVM Hypervisor                              |
| Runtime        |    | (pkg/vm/kvm/)                               |
| (pkg/container |    |                                             |
|  /host/)       |    |  +----------+  +---------+  +----------+   |
|                |    |  | vCPU     |  | Virtio  |  | UART     |   |
| - Namespaces   |    |  | Manager  |  | Devices |  | Console  |   |
| - OverlayFS    |    |  +----------+  +---------+  +----------+   |
| - Cgroups v2   |    |                                             |
| - Capabilities |    |  +----------+  +---------+  +----------+   |
| - PTY/Console  |    |  | Memory   |  | TAP     |  | IRQ/GIC  |   |
| - Signal proxy |    |  | Manager  |  | Network |  | Manager  |   |
+------+---------+    |  +----------+  +---------+  +----------+   |
       |              +------+-------------------------------------+
       |                     |
       v                     v
+------+---------+    +------+-------------------------------------+
| Host Network   |    | Guest VM (PID 1 = sandal binary)            |
| (pkg/container |    | (pkg/vm/guest/)                             |
|  /net/)        |    |                                             |
|                |    |  - Module loading (virtio_mmio, fuse, ...)  |
|                |    |  - VirtioFS mounts                          |
| - sandal0      |    |  - Network config                          |
|   bridge       |    |  - Chroot to tmpfs root                    |
| - veth pairs   |    |  - Re-exec sandal CLI inside VM            |
| - DHCP client  |    |      |                                     |
| - IP alloc     |    |      +---> Container Runtime (same as left)|
+---------+------+    +------+-------------------------------------+
          |                  |
          +--------+---------+
                   |
                   v
            +------+------+
            | sandal0     |
            | bridge      |
            | (shared L2) |
            +-------------+
```

## Two Execution Modes

### Mode 1: Direct Container (no `--vm`)

```
sandal run alpine:latest /bin/sh
```

The binary runs on the host. It pulls the OCI image, sets up overlayfs, creates namespaces (mount, PID, network, IPC, UTS), configures a veth pair on the sandal0 bridge, drops capabilities, and exec's the user command inside the container.

**Key path**: `cmd.Main()` -> `sandal.Run()` -> `sandal.RunContainer()` -> `host.Run()` -> fork -> `guest.ContainerInitProc()`

### Mode 2: VM-Isolated Container (`--vm`)

```
sandal run --vm alpine:latest /bin/sh
```

The binary creates a KVM virtual machine, boots a Linux kernel inside it, and re-executes itself as PID 1 (init) in the VM. The VM init process then runs the container inside the VM using the same container runtime.

**Key path**: `cmd.Main()` -> `sandal.Run()` -> `sandal.RunInVM()` -> `sandal.RunInKVM()` -> `kvm.Boot()` -> [VM boots] -> `VMInit()` -> `cmd.Main()` -> `sandal.RunContainer()`

**With daemon (`-d -startup`)**: The CLI delegates to the daemon instead of booting directly. The daemon health check detects the container has no running PID and calls `sandal.Run(HostArgs)` to boot the VM in a forked child process. This enables auto-restart: if the VM process dies, the daemon detects it and re-boots.

**Key path (daemon)**: CLI -> `RunInKVM()` -> delegate -> daemon health check -> `sandal.Run()` -> `RunInKVM()` -> `forkVMProcess()` -> child: `sandal vm start` -> `kvm.Boot()`

### Why the Same Binary?

Sandal embeds itself into the VM's initrd as `/init`. When the kernel boots, it runs the sandal binary as PID 1. The binary detects it's running as VM init (`IsVMInit()`: PID == 1 && SANDAL_VM_ARGS is set) and enters the guest initialization path before re-dispatching the original CLI command.

## Cross-Platform Support

| Platform | Hypervisor | Container Runtime | Notes |
|----------|-----------|-------------------|-------|
| Linux ARM64 | KVM (pkg/vm/kvm/) | Full | Primary target |
| Linux x86-64 | KVM (pkg/vm/kvm/) | Full | Supported |
| macOS ARM64 | Virtualization.framework (pkg/vm/vz/) | Via VM only | Requires CGO |

## Key Abstractions

### 1. Container Config (`pkg/container/config/`)

Central configuration struct passed through the container lifecycle. Both direct containers and VM-isolated containers use this same struct for unified state management:

```go
type Config struct {
    Name        string              // Unique container name
    HostPid     int                 // Host-side PID (VM: KVM child process PID)
    ContPid     int                 // Container-side PID (unused for VM containers)
    RootfsDir   string              // Mounted root filesystem path
    ChangeDir   string              // Overlay upper/work directory
    NS          namespace.Namespaces // Namespace configuration
    Capabilities capabilities.Capabilities
    Net         any                 // Network interface specs
    VM          string              // VM mode: "" (direct), "kvm", "vz"
    Volumes     wrapper.StringFlags // Bind mount specs (-v host:container)
    MemoryLimit string              // Cgroup memory limit (VM: used for VM RAM)
    CPULimit    string              // Cgroup CPU limit (VM: used for vCPU count)
    Env         wrapper.StringFlags // Environment variables
    ContArgs    []string            // Command to execute
    HostArgs    []string            // Original CLI args (used for daemon recovery)
    Background  bool                // Run in background (-d)
    Startup     bool                // Auto-restart via daemon (-startup)
}
```

The `VM` field enables the daemon to distinguish VM containers from direct containers for health checks and recovery. When `VM != ""`, the daemon monitors `HostPid` (the KVM child process) instead of `ContPid`.

### 2. VM Config (`pkg/vm/config/`)

```go
type VMConfig struct {
    KernelPath  string         // Path to Linux kernel image
    InitrdPath  string         // Path to initramfs (contains sandal binary)
    DiskPath    string         // Raw disk image for guest root
    ISOPath     string         // Read-only ISO image
    Mounts      []VirtioMount  // VirtioFS host-to-guest mounts
    CPUCount    int            // Number of virtual CPUs
    MemoryBytes uint64         // Guest RAM size
    CommandLine string         // Kernel boot parameters
}
```

### 3. Virtio Device Interface (`pkg/vm/kvm/`)

```go
type virtioDevice interface {
    DeviceID() uint32
    Features() uint64
    ConfigRead(offset, size uint32) uint32
    ConfigWrite(offset, size, val uint32)
    HandleQueue(queueIdx int, dev *virtioMMIODev)
    Tag() string
}
```

All virtio devices (console, net, block, fs) implement this interface and are registered with the MMIO transport layer.

## VirtioFS State Isolation

The sandal library directory (`/var/lib/sandal/`) is shared into the VM guest via VirtioFS so the guest can access cached images, rootfs directories, and overlay change directories. However, this directory also contains the state subdirectory (`/var/lib/sandal/state/`) where container configs are persisted as JSON files.

Without protection, the container runtime running **inside** the VM guest would write its own state files through VirtioFS into the host's state directory, creating "ghost" container entries visible from the host:

```
HOST                                        VM GUEST (via VirtioFS)
/var/lib/sandal/                            /var/lib/sandal/
  ├── state/                                  ├── state/
  │   ├── myvm.json       ◄── host writes     │   ├── myvm.json     (same file)
  │   └── xyzrandom.json  ◄── guest writes!   │   └── xyzrandom.json
  ├── image/                                  ├── image/
  │   └── alpine_*.sqfs   (shared, read)      │   └── alpine_*.sqfs
  └── changedir/                              └── changedir/
      └── myvm/            (shared, r/w)          └── myvm/
```

### Solution: State Directory Redirect

**Files**: `main_linux.go`, `pkg/env/get.go`

After `VMInit()` completes in `main_linux.go`, the state directory is redirected to a local tmpfs path (`/tmp/sandal-state`). This allows the container runtime inside the VM to write config files that child processes need to read, without creating ghost entries on the host via VirtioFS.

```
main_linux.go (VM guest path):

  guest.VMInit()                                      ◄── Stage 1-6: mount filesystems, VirtioFS
  env.SetDefault("SANDAL_STATE_DIR", "/tmp/sandal-state")  ◄── Redirect state to tmpfs
  env.BaseStateDir = "/tmp/sandal-state"
  cmd.Main()                                          ◄── Re-dispatch: sandal run → RunContainer()
    └── RunContainer()
          ├── SetContainer(c) → writes to /tmp/sandal-state/  ◄── Local tmpfs, not VirtioFS
          └── host.Run(c)
                └── crun()
                      └── SetContainer(c) → writes to /tmp/sandal-state/
```

`env.SetDefault()` updates both the `env.BaseStateDir` variable and the defaults list used by `appendSandalVariables()` to propagate environment to child processes. This ensures the spawned container child reads config from the same redirected path.

The host-side `SetContainer()` calls in `RunInKVM()` are unaffected since they run outside the VM where `SANDAL_STATE_DIR` points to the real state directory.

## Data Flow: `sandal run --vm -v /data alpine:latest /bin/sh`

```
1. CLI parses flags
2. Pull alpine:latest -> squashfs cache (~/.sandal-vm/images/)
3. Download Linux kernel -> cache (~/.sandal-vm/kernel/)
4. Build initrd: embed sandal binary + kernel modules
5. Build kernel cmdline: SANDAL_VM_ARGS=<base64(["run","alpine:latest","/bin/sh"])>
6. Create sandal0 bridge, allocate IP from subnet
7. Create KVM VM:
   a. Open /dev/kvm
   b. Allocate guest RAM (mmap MAP_NORESERVE)
   c. Load kernel + initrd into guest memory
   d. Create vCPUs, set registers (ARM64: PC=kernel entry, X0=DTB addr)
   e. Create virtio devices:
      - virtio-console (stdin/stdout relay)
      - virtio-fs tag=fs-0 hostPath=/data
      - virtio-fs tag=sandal-lib hostPath=~/.sandal-vm/
      - virtio-net (backed by TAP on sandal0 bridge)
   f. Build DTB with device nodes
   g. Setup GIC + IRQ routing + eventfd injection
8. Start vCPU threads (KVM_RUN loop)
9. Guest kernel boots, runs /init (sandal binary)
10. VMInit():
    a. Mount /proc, /sys, /dev
    b. Load modules: virtio_mmio, fuse, virtiofs, overlay, ...
    c. Mount virtiofs: mount -t virtiofs fs-0 /mnt/data
    d. Configure eth0 from SANDAL_VM_NET
    e. Re-exec: sandal run alpine:latest /bin/sh
11. Container starts inside VM (namespaces, overlayfs, exec /bin/sh)
12. User interacts via virtio-console <-> host terminal
```

## State Storage

| Path | Content |
|------|---------|
| `/var/lib/sandal/state/` | Container config JSON files (both direct and VM containers) |
| `/var/run/sandal/` | Runtime files (PID files, sockets) |
| `~/.sandal-vm/machines/<name>/` | Ephemeral VM config (kernel/initrd paths, created at boot, deleted on exit) |
| `~/.sandal-vm/kernel/` | Cached Linux kernel images |
| `~/.sandal-vm/images/` | Cached OCI images (squashfs) |
| `$SANDAL_ROOTFSDIR/<name>/` | Container root filesystems |
| `$SANDAL_CHANGE_DIR/<name>/` | Overlay upper/work directories |

VM containers are tracked in the same state directory as direct containers. The container config's `VM` field distinguishes them. The ephemeral VM config under `~/.sandal-vm/machines/` is only used during the VM boot process and is cleaned up when the VM exits.
