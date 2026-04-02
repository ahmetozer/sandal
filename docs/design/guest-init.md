# Guest VM Initialization

When sandal runs inside a KVM VM as PID 1, it performs system initialization before re-dispatching the original CLI command. This document describes the guest-side boot process.

## Detection

**File**: `pkg/vm/guest/init.go`

```go
func IsVMInit() bool {
    return os.Getpid() == 1 && os.Getenv("SANDAL_VM_ARGS") != ""
}
```

Three conditions identify VM init mode:
1. Running as PID 1 (init process)
2. `SANDAL_VM_ARGS` environment variable is set (base64-encoded CLI args)
3. `/proc/cmdline` contains SANDAL_* parameters

## Boot Stages

### Stage 0: Preinit (ARM64 only)

Before the Go binary can run, a tiny static ELF (`preinit_arm64`) handles minimal setup:

```
Kernel boots -> /init (preinit binary)
  1. mount("proc", "/proc", "proc")
  2. mount("devtmpfs", "/dev", "devtmpfs")
  3. open("/dev/console") -> fd 0,1,2
  4. execve("/sandal-init")  -> sandal Go binary takes over
```

This is needed because Go runtime requires `/proc/self/exe` and valid stdio file descriptors.

On x86-64, the kernel's built-in devtmpfs and console setup makes this unnecessary.

### Stage 1: Environment Import

```go
func importKernelCmdlineEnv() {
    cmdline, _ := os.ReadFile("/proc/cmdline")
    // Parse space-separated key=value pairs
    // For SANDAL_* keys: base64-decode the value
    // Set as environment variables
}
```

**Kernel command line parameters**:

| Parameter | Format | Description |
|-----------|--------|-------------|
| `SANDAL_VM_ARGS` | base64(JSON array) | Original CLI arguments |
| `SANDAL_VM_NET` | base64(JSON object) | Network configuration |
| `SANDAL_VM_MOUNTS` | comma-separated | VirtioFS mount specs |
| `SANDAL_VM` | `"kvm"` or `"mac"` | Hypervisor type |
| `SANDAL_LIB_DIR` | path | Library directory (for image cache) |
| `console` | `ttyAMA0`/`ttyS0` | Kernel console device |
| `loglevel` | `0`-`7` | Kernel log verbosity |

### Stage 2: Filesystem Setup

```
mount("proc",     "/proc",    "proc",     0, "")
mount("devtmpfs", "/dev",     "devtmpfs", 0, "")
mount("sysfs",    "/sys",     "sysfs",    0, "")
mount("devpts",   "/dev/pts", "devpts",   0, "")
```

Redirect stdio to console:
```go
console, _ := os.OpenFile("/dev/console", os.O_RDWR, 0)
unix.Dup2(int(console.Fd()), 0)  // stdin
unix.Dup2(int(console.Fd()), 1)  // stdout
unix.Dup2(int(console.Fd()), 2)  // stderr
```

### Stage 3: Kernel Module Loading

**File**: `pkg/lib/modprobe/`

Modules are loaded in dependency order. `virtio_mmio` **must** be loaded first as it's the transport for all virtio devices.

```
Group 1 - Virtio transport (MUST be first):
  virtio_mmio

Group 2 - Storage:
  fuse           # FUSE protocol support
  virtiofs       # VirtioFS filesystem type
  overlay        # OverlayFS for container layers
  loop           # Loop device for disk images
  squashfs       # SquashFS filesystem (compressed images)
  ext4           # EXT4 filesystem

Group 3 - Networking:
  veth           # Virtual ethernet pairs
  bridge         # Linux bridge
  tun            # TUN/TAP devices
  af_packet      # Raw packet sockets

Group 4 - Netfilter (for container NAT):
  nf_conntrack
  nf_nat
  nf_tables
  iptable_nat
  iptable_filter
  ip_tables
  ip_vs
  ip_vs_rr

Group 5 - Tunneling:
  vxlan
  ip_gre
  wireguard
  ip6_tunnel
```

Module loading uses:
```go
func LoadModule(name string) error {
    f, _ := os.Open("/lib/modules/<version>/" + name + ".ko")
    data, _ := io.ReadAll(f)
    unix.SyscallNoError(unix.SYS_INIT_MODULE, uintptr(unsafe.Pointer(&data[0])),
        uintptr(len(data)), 0)
}
```

### Stage 4: Root Filesystem Setup

The initial root is an initramfs (read-only, in-memory). Since pivot_root doesn't work on initramfs, sandal creates a tmpfs root:

```
mount("tmpfs", "/newroot", "tmpfs", 0, "")

Copy files to new root:
  /newroot/init         <- /init (sandal binary)
  /newroot/proc/
  /newroot/sys/
  /newroot/dev/
  /newroot/mnt/
  /newroot/tmp/
  /newroot/var/lib/sandal/
  /newroot/var/run/sandal/

chroot("/newroot")
chdir("/")
```

### Stage 5: VirtioFS Mounting

```
Decode SANDAL_VM_MOUNTS:
  "fs-0=/host/data,fs-1=/home/user/.sandal-vm/images,fs-2=/home/user/.sandal-vm/kernel"

For each mount spec:
  Parse: tag=hostpath  or  tag=hostpath=guestmountpoint

  Default guestmountpoint = /mnt/<hostpath>

  mkdir -p <guestmountpoint>
  mount(tag, guestmountpoint, "virtiofs", 0, "")
```

After VirtioFS mounts, the guest can access:
- Host directories shared via `-v` flags
- OCI image cache (for container rootfs)
- Sandal library directory

### Stage 6: Network Configuration

```
Decode SANDAL_VM_NET (JSON):
{
  "addr": "172.19.0.5/24",
  "gateway": "172.19.0.1",
  "mac": "52:54:00:12:34:01",
  "mtu": 1500
}

Configure:
  ip link set eth0 address <mac>
  ip addr add <addr> dev eth0
  ip link set eth0 up
  ip link set lo up
  ip route add default via <gateway>
```

### Stage 7: State Isolation and CLI Re-dispatch

After VMInit() returns, `main_linux.go` disables state writes and calls `cmd.Main()`:

```go
func platformMain() {
    if guest.IsVMInit() {
        guest.VMInit()                        // Stages 1-6 above
        controller.DisableStateWrites = true  // Prevent ghost entries
        cmd.Main()                            // Re-dispatch original command
        return
    }
    // ...
}
```

The `DisableStateWrites` flag is critical because the state directory (`/var/lib/sandal/state/`) is shared into the VM via VirtioFS as part of the sandal library directory. Without this flag, `RunContainer()` and the container runtime would write state JSON files through VirtioFS, creating ghost container entries visible from the host alongside the real VM container entry. See [architecture.md](architecture.md#virtiofs-state-isolation) for the full explanation.

`cmd.Main()` reads `os.Args` which were reconstructed from `SANDAL_VM_ARGS`:
```
Original host command:  sandal run --vm --name myvm -d -v /data alpine:latest /bin/sh
VM_ARGS decoded:        ["run", "-v", "/mnt/data", "alpine:latest", "/bin/sh"]
```

Note that host-only flags (`--vm`, `--name`, `-d`, `-startup`, `--cpu`, `--memory`) are stripped before encoding into `SANDAL_VM_ARGS`. Paths are translated from host paths to guest VirtioFS mount paths by `guest.ResolvePath()`.

## Path Resolution

**File**: `pkg/vm/guest/resolve.go`

```go
func ResolvePath(hostPath string, mounts []MountSpec) string {
    for _, m := range mounts {
        if strings.HasPrefix(hostPath, m.HostPath) {
            relative := strings.TrimPrefix(hostPath, m.HostPath)
            return filepath.Join(m.GuestPath, relative)
        }
    }
    return hostPath  // fallback: return unchanged
}
```

Example:
```
Host path:       /home/user/.sandal-vm/images/alpine.sqsh
Mount spec:      fs-1=/home/user/.sandal-vm/images=/mnt/images
Resolved path:   /mnt/images/alpine.sqsh
```

## Environment Variables in Guest

| Variable | Set By | Value |
|----------|--------|-------|
| `SANDAL_VM` | kernel cmdline | `"kvm"` |
| `SANDAL_VM_ARGS` | kernel cmdline | base64 JSON of original args |
| `SANDAL_VM_NET` | kernel cmdline | base64 JSON of network config |
| `SANDAL_VM_MOUNTS` | kernel cmdline | Mount specifications |
| `SANDAL_LIB_DIR` | derived | VirtioFS-mounted library path |
| `SANDAL_RUN_DIR` | defaults | `/var/run/sandal/` |
| `SANDAL_ROOTFSDIR` | defaults | Derived from lib dir |

## Shutdown

When the container exits inside the VM:
1. Container process exits
2. sandal CLI exits
3. PID 1 (sandal init) exits
4. Kernel performs orderly shutdown
5. Guest sends PSCI SYSTEM_OFF (ARM64) or shutdown (x86)
6. KVM exit reason: `kvmExitSystemEvent` or `kvmExitShutdown`
7. Host `runVCPU()` loop exits
8. `vm.Stop()` is called
9. `vm.WaitUntilStopped()` returns
10. Terminal restored, host process exits
