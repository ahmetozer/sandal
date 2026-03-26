# Sandal Design Documentation

This directory contains the design documentation for the Sandal project. These documents describe the architecture, subsystems, data flows, and key abstractions so that AI agents and developers can understand the system without reading every source file.

## Document Index

| Document | Description |
|----------|-------------|
| [architecture.md](architecture.md) | High-level architecture, component map, and data flow |
| [boot-sequence.md](boot-sequence.md) | End-to-end boot flow from CLI invocation to user workload |
| [container-runtime.md](container-runtime.md) | Linux namespace isolation, overlayfs, cgroups, capabilities |
| [kvm-hypervisor.md](kvm-hypervisor.md) | KVM ioctl layer, vCPU lifecycle, memory model, ARM64/x86 specifics |
| [virtio-devices.md](virtio-devices.md) | Virtio MMIO transport, virtqueue model, device implementations |
| [virtiofs-fuse.md](virtiofs-fuse.md) | Built-in FUSE server, VirtioFS protocol, file operation handling |
| [networking.md](networking.md) | Bridge networking, TAP devices, veth pairs, DHCP, IP allocation |
| [image-management.md](image-management.md) | OCI image pulling, squashfs, caching, kernel/initrd generation |
| [guest-init.md](guest-init.md) | VM guest initialization, module loading, virtiofs mounting |
| [daemon-controller.md](daemon-controller.md) | Daemon lifecycle, IPC protocol, container state persistence |
| [cli-commands.md](cli-commands.md) | CLI command structure, flag parsing, subcommand dispatch |

## How to Read These Documents

**For understanding what Sandal does**: Start with [architecture.md](architecture.md).

**For understanding a specific subsystem**: Jump directly to the relevant document.

**For understanding the full lifecycle of `sandal run --vm`**: Read [boot-sequence.md](boot-sequence.md).

**For modifying virtio devices or KVM code**: Read [kvm-hypervisor.md](kvm-hypervisor.md) and [virtio-devices.md](virtio-devices.md).

## Repository Layout

```
sandal/
  main.go                          # Entry point, delegates to platformMain()
  main_linux.go                    # Linux: VM init / container init / host mode dispatch
  main_darwin.go                   # macOS: Apple Virtualization Framework path
  pkg/
    cmd/                           # CLI subcommand implementations
      run/                         # "sandal run" command (container + VM paths)
    container/
      config/                      # Container configuration types
      host/                        # Host-side container lifecycle (fork, exec, cleanup)
      guest/                       # Container init process (PID 1 inside container)
      runtime/                     # Shared types, constants, utilities
      namespace/                   # Linux namespace management
      net/                         # Container network setup (veth, bridge, DHCP)
      capabilities/                # Linux capability management
      overlayfs/                   # OverlayFS upper/work directory handling
      diskimage/                   # Disk image detection and mounting
      resources/                   # Cgroup v2 resource limits
      console/                     # PTY/FIFO/socket console handling
      snapshot/                    # Container filesystem snapshots
    controller/                    # IPC server/client for daemon communication
    daemon/                        # Background daemon (bridge, health, signals)
    env/                           # Environment variable defaults and detection
    lib/
      container/image/             # OCI image pulling and flattening
      container/registry/          # OCI registry V2 client
      squashfs/                    # SquashFS reader/writer
      dhcp/                        # DHCPv4/v6 client
      loopdev/                     # Loop device attachment
      img/                         # GPT/MBR partition table parsing
      modprobe/                    # Kernel module loading
      alpine/                      # Alpine Linux APK integration
      zstd/                        # ZSTD decompression
      inotify/                     # Filesystem event watching
      mkfs/                        # Filesystem creation
      detectFs/                    # Filesystem type detection
      cmdLine/                     # Kernel cmdline parsing
    vm/
      config/                      # VM configuration types
      disk/                        # Raw disk image creation
      guest/                       # VM guest init (PID 1 in VM)
      kernel/                      # Kernel download, initrd/CPIO generation
      kvm/                         # KVM hypervisor (Linux)
      terminal/                    # Terminal raw mode handling
      vz/                          # Apple Virtualization Framework (macOS)
```

## Key Dependencies

| Dependency | Purpose |
|-----------|---------|
| `golang.org/x/sys/unix` | Linux/macOS syscall bindings (ioctl, mmap, mount, clone) |
| `github.com/vishvananda/netlink` | Netlink-based network interface management |
| Go standard library | Everything else (no CGO on Linux) |

## Build

```bash
make build          # Platform-specific build
make generate       # Generate embedded kernel resources
make build-linux    # Linux binary (CGO_ENABLED=0)
make build-darwin   # macOS binary (CGO_ENABLED=1)
```

Output: single static binary `sandal` (~12MB).
