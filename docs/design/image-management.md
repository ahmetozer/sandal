# Image Management

Sandal handles OCI container images, Linux kernels, and initrd generation. All artifacts are cached locally to avoid repeated downloads.

## OCI Container Images

### Registry Client

**Package**: `pkg/lib/container/registry/`

Implements the OCI Distribution Spec v2 API for pulling images from registries.

#### Image Reference Parsing

**File**: `registry/reference.go`

```
Input formats:
  "alpine"                      -> registry-1.docker.io/library/alpine:latest
  "alpine:3.18"                 -> registry-1.docker.io/library/alpine:3.18
  "ghcr.io/user/repo:v1"       -> ghcr.io/user/repo:v1
  "registry.io/img@sha256:abc" -> registry.io/img@sha256:abc
```

#### Authentication

**File**: `registry/auth.go`

```
1. Try unauthenticated request
2. If 401, parse WWW-Authenticate header:
   Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:library/alpine:pull"
3. Load credentials from ~/.docker/config.json
4. Exchange credentials for bearer token
5. Retry with Authorization: Bearer <token>
```

#### Pull Flow

**File**: `registry/client.go`

```
FetchManifest(ref)
  |
  +-- GET /v2/<name>/manifests/<reference>
  |   Accept: application/vnd.oci.image.index.v1+json,
  |           application/vnd.docker.distribution.manifest.list.v2+json,
  |           application/vnd.oci.image.manifest.v1+json,
  |           application/vnd.docker.distribution.manifest.v2+json
  |
  +-- If manifest list/index:
  |     Select platform (linux/arm64 or linux/amd64)
  |     Fetch platform-specific manifest
  |
  +-- Return manifest with layer digests and config
```

### Image Pulling and Flattening

**Package**: `pkg/lib/container/image/`

**File**: `image/pull.go`

```
Pull(imageRef) -> squashfsPath
  |
  +-- Check cache: ~/.sandal-vm/images/<sanitized_ref>.sqsh
  |     If cached, return cached path
  |
  +-- registry.FetchManifest(ref) -> manifest
  |
  +-- For each layer in manifest.Layers:
  |     GET /v2/<name>/blobs/<digest>
  |     Download and decompress (gzip/zstd)
  |
  +-- Flatten all layers into single directory:
  |     Apply layers in order (lower -> upper)
  |     Handle whiteout files (.wh.*)
  |
  +-- squashfs.Create(flatDir) -> squashfsPath
  |     Convert flattened directory to squashfs image
  |
  +-- Cache at ~/.sandal-vm/images/<sanitized_ref>.sqsh
  |
  +-- Return squashfsPath
```

### SquashFS

**Package**: `pkg/lib/squashfs/`

Sandal uses squashfs as its primary immutable image format:
- Compressed, read-only filesystem
- Efficient random access
- Small file size
- Mountable directly by Linux kernel

**File**: `squashfs/writer.go` - Implements squashfs v4.0 format:
- Gzip compression for data blocks
- Inode table, directory table, fragment table
- Proper superblock with magic `0x73717368` ("hsqs")

## Linux Kernel Management

**Package**: `pkg/vm/kernel/`

### Kernel Download

**File**: `kernel/kernel.go`

```
EnsureKernel() -> kernelPath
  |
  +-- Check cache: ~/.sandal-vm/kernel/<arch>/vmlinuz
  |     If cached, return cached path
  |
  +-- Determine architecture: arm64 or x86_64
  |
  +-- Fetch Alpine Linux APK index:
  |     https://dl-cdn.alpinelinux.org/alpine/latest-stable/main/<arch>/APKINDEX.tar.gz
  |
  +-- Find linux-virt package version
  |
  +-- Download APK:
  |     https://dl-cdn.alpinelinux.org/alpine/latest-stable/main/<arch>/linux-virt-<ver>.apk
  |
  +-- Extract kernel image from APK (tar.gz format):
  |     boot/vmlinuz-virt -> vmlinuz
  |
  +-- If ARM64: detect ZBOOT format, decompress if needed
  |     ZBOOT: EFI stub wrapping gzip-compressed kernel Image
  |     Magic: "zimg" at offset 4
  |     Extract and decompress to raw ARM64 Image
  |
  +-- Cache at ~/.sandal-vm/kernel/<arch>/vmlinuz
  |
  +-- Also extract kernel modules for initrd
  |     lib/modules/<version>/ -> cached for initrd generation
  |
  +-- Return kernelPath
```

### Initrd Generation

**File**: `kernel/initrd.go`

The initrd contains the sandal binary and kernel modules needed for the VM
to boot and mount VirtioFS shares. The result is **content-addressed**: the
cache key is `sha256(sandal binary || base initrd)` truncated to 16 bytes
hex, and the file lives at `<BaseKernelDir>/initramfs-sandal-<hash>.img`.
Identical inputs always resolve to the same path; on a cache hit no archive
is built.

```
CreateFromBinary(binData, baseInitrd) -> cachePath
  |
  +-- Compute cache key:
  |     sha256(binary) || 0x00 || sha256(baseInitrd)
  |     -> first 16 bytes hex -> "439a75c21ae9...".img
  |
  +-- If <BaseKernelDir>/initramfs-sandal-<hash>.img exists -> return it
  |
  +-- Build into a temp file in <BaseKernelDir> (same dir for atomic rename):
  |     - Decompress base initrd, strip CPIO trailer, append
  |     - Append /dev/console char device (5,1) so the kernel
  |       wires fds 0/1/2 when init is exec'd
  |     - Append /etc/{nsswitch.conf,resolv.conf,hosts} for Go runtime
  |     - Append sandal binary as /init
  |       (mounts /dev devtmpfs + /proc procfs itself in
  |        importKernelCmdlineEnv before any code that needs them)
  |     - Write CPIO trailer
  |     - Gzip compress
  |     - fsync
  |
  +-- Atomic rename to final cache path
  |
  +-- Return cachePath (owned by cache, NOT removed by caller)
```

The cache entry is reused across every VM start that uses the same sandal
binary and base initrd — typically every run on a stable host. Updating
sandal or upgrading the kernel produces a new entry; old entries stay on
disk and can be pruned manually with
`rm <BaseKernelDir>/initramfs-sandal-*.img`.

#### CPIO Format

**File**: `kernel/cpio.go`

Uses the "newc" (SVR4) CPIO format with ASCII hex headers:

```
Header (110 bytes):
  "070701"        magic
  ino, mode, uid, gid, nlink
  mtime, filesize
  devmajor, devminor, rdevmajor, rdevminor
  namesize, check

Followed by: filename (null-terminated, padded to 4 bytes)
Followed by: file data (padded to 4 bytes)
```

Functions:
- `writeCPIODir()` - Create directory entry
- `writeCPIOFile()` - Create file entry with content
- `writeCPIOCharDev()` - Create character device node
- `findInCPIO()` - Extract file from existing CPIO archive
- `stripCPIOTrailer()` - Remove "TRAILER!!!" entry for appending

### Initrd Overlay

**File**: `kernel/initrd.go` - `CreateOverlay()`

When the base initrd contains an init script (e.g., Alpine's init), sandal can inject VirtioFS mount commands:

```
CreateOverlay(baseInitrd, mounts) -> modifiedInitrd
  |
  +-- Find /init script in base CPIO
  |
  +-- Inject mount commands before "exec switch_root":
  |     mkdir -p /sysroot/mnt/data
  |     mount -t virtiofs fs-0 /sysroot/mnt/data
  |
  +-- Append modified init script as CPIO overlay
  |
  +-- Return combined initrd
```

### ZBOOT Extraction

**File**: `kernel/zboot.go`

ARM64 kernels from Alpine are in EFI ZBOOT format (gzip-compressed kernel wrapped in an EFI PE stub):

```
Detection:
  offset 0: MZ (PE header)
  offset 4: "zimg" (ZBOOT magic)

Extraction:
  Read payload_offset and payload_size from header
  Slice payload from file
  Decompress with gzip
  Result: raw ARM64 Image format kernel
```

## Cache Structure

```
~/.sandal-vm/
  kernel/
    arm64/
      vmlinuz               # Cached kernel image
      modules/              # Kernel modules for initrd
    amd64/
      vmlinuz
      modules/
  images/
    alpine_latest.sqsh      # Cached OCI images as squashfs
    ubuntu_22.04.sqsh
  machines/
    <vm-name>/
      config.json           # VM configuration
      pid                   # VM process PID
```

## Image Format Support Matrix

| Format | Container (direct) | Container (VM) | Notes |
|--------|-------------------|----------------|-------|
| OCI image reference | Pull + squashfs cache | Pull + squashfs cache | Auto-detected from string |
| SquashFS file | Loop mount | VirtioFS share | Preferred for immutable images |
| Directory | Direct lowerdir | VirtioFS share | Development use |
| Raw disk (MBR/GPT) | Loop mount + partition | VirtioFS or virtio-blk | For full disk images |
| ISO | Loop mount (read-only) | Virtio-blk (read-only) | Used for install media |
