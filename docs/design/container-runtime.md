# Container Runtime

The container runtime (`pkg/container/host/` and `pkg/container/guest/`) implements Linux container isolation using namespaces, overlayfs, cgroups v2, and capabilities. It runs identically on bare metal and inside KVM VMs.

## Overview

```
host.Run(config)
  |
  +-- Setup console (PTY or FIFO)
  +-- Create cgroup hierarchy
  +-- Fork child with clone flags
  +-- Parent: relay I/O, forward signals, wait
  +-- Child: ContainerInitProc() -> setup isolation -> exec user command
```

## Namespace Management

**Package**: `pkg/container/namespace/`

### Supported Namespaces

| Namespace | Clone Flag | Purpose |
|-----------|-----------|---------|
| `mnt` | `CLONE_NEWNS` | Mount isolation |
| `pid` | `CLONE_NEWPID` | Process ID isolation |
| `net` | `CLONE_NEWNET` | Network stack isolation |
| `ipc` | `CLONE_NEWIPC` | IPC isolation |
| `uts` | `CLONE_NEWUTS` | Hostname isolation |
| `user` | `CLONE_NEWUSER` | User/group ID mapping |
| `cgroup` | `CLONE_NEWCGROUP` | Cgroup isolation |

### Namespace Configuration

Each namespace can be set to:
- **Empty/new**: Create a fresh namespace (default for mnt, pid, ipc, uts, net)
- **`"host"`**: Share the host namespace (no isolation)
- **PID reference**: Join an existing container's namespace (`/proc/<pid>/ns/<type>`)
- **File path**: Join a namespace from a bind-mounted ns file

**File**: `namespace/allocate.go` - Sets defaults (new namespace for each type unless explicitly set to "host")

**File**: `namespace/set.go` - Joins existing namespaces via `setns()` syscall

**File**: `namespace/unshare.go` - Creates new namespaces via `unshare()` syscall

### How Clone Flags Are Built

```go
// namespace/namespace.go
func (ns Namespaces) CloneFlags() uintptr {
    var flags uintptr
    for name, val := range ns {
        if val == "" {  // empty = create new
            flags |= nameToFlag[name]  // e.g., CLONE_NEWPID
        }
    }
    return flags
}
```

## Filesystem Layer

### OverlayFS Setup

**Package**: `pkg/container/overlayfs/`

Containers use Linux overlayfs for copy-on-write filesystem semantics:

```
+-------------------+
| Upper (changedir) |  Writable layer - container modifications go here
+-------------------+
| Lower (rootfs)    |  Read-only layer - the container image (squashfs/dir/img)
+-------------------+
| Merged view       |  What the container process sees
+-------------------+
```

**Directory structure**:
```
$SANDAL_CHANGE_DIR/<container-name>/
  upper/     # Modified/new files
  work/      # OverlayFS work directory (internal)
```

**File**: `overlayfs/changes.go`
- `PrepareChangeDir()` determines the change directory type:
  - **Folder mode**: Direct upper/work directories on disk
  - **Image mode**: Creates a disk image + loop device for upper (used when host fs is itself overlayfs or doesn't support overlayfs)
  - **Tmpfs mode**: Mounts tmpfs for ephemeral changes (flag `-tmp`)

**File**: `host/rootfs.go`
- Parses lower directories from `-lw` flag
- Resolves OCI image references to local paths
- Detects image format (directory, squashfs, disk image with partitions)
- Mounts overlayfs: `mount("overlay", merged, "overlay", 0, "lowerdir=X,upperdir=Y,workdir=Z")`

### Disk Image Support

**Package**: `pkg/container/diskimage/`

Sandal can use various image formats as the lower (read-only) layer:

| Format | Detection | Mount Method |
|--------|----------|--------------|
| Directory | `os.Stat().IsDir()` | Direct use as lowerdir |
| SquashFS | Magic bytes at offset 0 | `mount -t squashfs` via loop device |
| Raw disk (MBR) | MBR signature `0x55AA` | Parse partition table, loop mount with offset |
| Raw disk (GPT) | GPT header signature | Parse GPT entries, loop mount with offset |

**File**: `diskimage/detect.go` - Probes first bytes to determine type

**File**: `diskimage/image.go` - Calculates partition byte offsets for `disk.img:part=2` syntax

### Mount Setup

**File**: `guest/mount.go`

After overlayfs is mounted, the init process sets up:

```
/proc         - procfs (with meminfo/cpuinfo overrides matching cgroup limits)
/sys          - sysfs
/dev          - devtmpfs
/dev/pts      - devpts
/dev/shm      - tmpfs
/tmp          - tmpfs
/dev/null     - character device 1:3
/dev/zero     - character device 1:5
/dev/full     - character device 1:7
/dev/random   - character device 1:8
/dev/urandom  - character device 1:9
/dev/tty      - character device 5:0
```

**Pivot Root** isolates the container's view:
```go
// mount.go
unix.Mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, "")
syscall.PivotRoot(newRoot, pivotDir)
unix.Unmount(pivotDir, unix.MNT_DETACH)
```

### Volume Mounts

```go
// mount.go - for each -v /host/path:/container/path
unix.Mount(hostPath, containerPath, "", unix.MS_BIND|unix.MS_REC, "")
```

Volume paths are validated to prevent escaping the container rootfs via `..` traversal.

## Capability Management

**Package**: `pkg/container/capabilities/`

### Default Capabilities (non-privileged)

```
CHOWN, DAC_OVERRIDE, FSETID, FOWNER, MKNOD, NET_RAW,
SETGID, SETUID, SETFCAP, SETPCAP, NET_BIND_SERVICE,
SYS_CHROOT, KILL, AUDIT_WRITE
```

### Privileged Mode

All 40+ capabilities are granted when `--privileged` is specified.

### Capability Dropping

**File**: `capabilities/set.go`

```
SetCapabilities(caps)
  |
  +-- For each capability NOT in the allowed set:
  |     prctl(PR_CAPBSET_DROP, cap) -> remove from bounding set
  |
  +-- Build capability header + data:
  |     Set Effective = Permitted = Inheritable = allowed caps bitmask
  |     capset(header, data)
  |
  +-- For each allowed capability:
        prctl(PR_CAP_AMBIENT_RAISE, cap) -> allow inheritance across execve
```

## Cgroup v2 Resource Limits

**Package**: `pkg/container/resources/`

**File**: `resources/resources.go`

```
SetLimits(name, memLimit, cpuLimit)
  |
  +-- mkdir /sys/fs/cgroup/sandal/<name>/
  |
  +-- If memLimit set:
  |     Write memLimit to /sys/fs/cgroup/sandal/<name>/memory.max
  |     Generate override /proc/meminfo content (matching limit)
  |
  +-- If cpuLimit set:
  |     Parse "2" -> "200000 100000" (200ms quota per 100ms period)
  |     Write to /sys/fs/cgroup/sandal/<name>/cpu.max
  |     Generate override /proc/cpuinfo (matching CPU count)
  |
  +-- Write container PID to cgroup.procs
```

Memory limit format: integer + optional suffix (K, M, G, T). Example: `512M`, `2G`.

CPU limit format: integer representing number of CPUs. Example: `2` means 200ms quota per 100ms period.

## Process Lifecycle

### Fork and Exec

**File**: `host/crun.go`

```go
cmd := exec.Command("/proc/self/exe")  // Re-exec self
cmd.Env = append(os.Environ(), "SANDAL_CHILD="+cfg.Name)
cmd.SysProcAttr = &syscall.SysProcAttr{
    Cloneflags: cfg.NS.CloneFlags(),  // CLONE_NEWPID | CLONE_NEWNS | ...
    Pdeathsig:  syscall.SIGKILL,      // Kill child if parent dies
}
cmd.Start()
```

The child process detects `SANDAL_CHILD` env var and enters `ContainerInitProc()`.

### Signal Forwarding

**File**: `host/crun.go`

The parent process forwards signals to the container:
```go
signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, ...)
go func() {
    for sig := range sigCh {
        cmd.Process.Signal(sig)
    }
}()
```

### Console Handling

**File**: `host/pty_linux.go`, `container/console/`

Two console modes:

1. **PTY mode** (foreground, `-t`): Allocates master/slave PTY pair. Parent relays between host terminal and PTY master. Container stdin/stdout/stderr connected to PTY slave.

2. **FIFO mode** (daemon, `-d`): Creates named pipes at `$SANDAL_RUN_DIR/<name>/stdin` and `$SANDAL_RUN_DIR/<name>/stdout`. The `attach` command connects to these FIFOs.

### User Switching

**File**: `guest/user.go`

```
switchUser(spec)   // spec = "user:group" or "uid:gid"
  |
  +-- Resolve user/group names to UID/GID (from /etc/passwd, /etc/group)
  +-- prctl(PR_SET_KEEPCAPS, 1) -> preserve caps across setuid
  +-- setresgid(gid, gid, gid)
  +-- setresuid(uid, uid, uid)
  +-- Re-apply capabilities (ambient caps for non-root)
```

## Container Config

**Package**: `pkg/container/config/`

**File**: `config/definitions.go` (cross-platform — type dependencies in `namespace/types.go`, `capabilities/types.go`, `diskimage/types.go`, `wrapper/flags.go` are also cross-platform)

```go
type Config struct {
    Name          string                        // Unique identifier
    Created       int64                         // Unix timestamp
    HostPid       int                           // Host PID of container process
    ContPid       int                           // PID inside container (usually 1)
    TmpSize       uint                          // Tmpfs size in MB (0 = disabled)
    ChangeDirSize string                        // Change dir disk image size (e.g. "128m")
    ChangeDirType string                        // "auto", "folder", "image"
    ChangeDir     string                        // Overlay upper directory path
    RootfsDir     string                        // Merged overlay mount point
    Snapshot      string                        // Snapshot output path
    ReadOnly      bool                          // Read-only rootfs
    Remove        bool                          // Remove container files on exit
    EnvAll        bool                          // Pass all host env vars
    Background    bool                          // Run in background (-d)
    Startup       bool                          // Auto-start on daemon boot
    TTY           bool                          // Allocate PTY
    NS            namespace.Namespaces          // Namespace config map
    Capabilities  capabilities.Capabilities     // Allowed Linux capabilities
    User          string                        // User:group to run as
    Devtmpfs      string                        // Mount point of devtmpfs
    Resolv        string                        // Resolver configuration
    Hosts         string                        // Hosts file handling
    Status        string                        // Container status
    Dir           string                        // Working directory
    Volumes       wrapper.StringFlags           // -v mount specifications
    ImmutableImages diskimage.ImmutableImages   // Mounted immutable images
    HostArgs      []string                      // Original host-side args
    ContArgs      []string                      // Command to run in container
    Lower         wrapper.StringFlags           // Additional overlay lower dirs
    RunPreExec    wrapper.StringFlags           // Run commands before init
    RunPrePivot   wrapper.StringFlags           // Run commands before pivoting
    PassEnv       wrapper.StringFlags           // Pass specific env vars
    Net           any                           // Network interface specifications
    Ports         []forward.PortMapping         // Port forwarding mappings
    VM            string                        // "" = no VM, "kvm" = KVM, "vz" = VZ
    MemoryLimit   string                        // Memory cgroup limit
    CPULimit      string                        // CPU cgroup limit
}
```

**Container naming**: `config/tools.go`
- Validation: alphanumeric, dots, dashes, underscores, max 128 chars
- Auto-generated ID: base-62 encoded (timestamp + random suffix)

## Cleanup

**File**: `host/derun.go`

```
Cleanup(cfg)
  |
  +-- Unmount overlay filesystem
  +-- Unmount any loop-mounted images
  +-- Remove cgroup directory
  +-- Remove console FIFOs/sockets
  +-- Remove container state file
  +-- Optionally remove rootfs and change directories
```
