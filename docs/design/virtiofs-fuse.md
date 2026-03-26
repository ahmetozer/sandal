# VirtioFS and FUSE Server

Sandal implements a built-in VirtioFS device with an embedded FUSE server. This eliminates the need for an external `virtiofsd` daemon - file operations are handled directly by the sandal process.

## Overview

```
Guest Kernel                          Host (sandal process)
+------------------+                  +---------------------------+
| Application      |                  | VirtioFSDevice            |
|   read("/data")  |                  |                           |
+--------+---------+                  |  +---------------------+  |
         |                            |  | FUSE Request Handler|  |
+--------+---------+                  |  |                     |  |
| VFS Layer        |                  |  | handleFuseRequest() |  |
+--------+---------+                  |  +----------+----------+  |
         |                            |             |              |
+--------+---------+                  |  +----------+----------+  |
| FUSE Kernel      |  <--virtqueue--> |  | Node/File Handle   |  |
| Module           |                  |  | Management         |  |
| (virtiofs type)  |                  |  +----------+----------+  |
+------------------+                  |             |              |
                                      |  +----------+----------+  |
                                      |  | Host Filesystem     |  |
                                      |  | (os.Open, os.Read)  |  |
                                      |  +---------------------+  |
                                      +---------------------------+
```

## Device Structure

**File**: `pkg/vm/kvm/virtiofs.go`

```go
type VirtioFSDevice struct {
    tag      string          // Mount tag (e.g., "fs-0")
    hostPath string          // Host directory to share
    readOnly bool            // Disable write operations

    // Node management
    mu       sync.Mutex
    nodes    map[uint64]string   // nodeID -> host path
    nextNode uint64              // Next node ID to allocate

    // File handle management
    handles    map[uint64]*os.File  // fh -> open file
    nextHandle uint64               // Next file handle to allocate
}
```

### Virtio Config

- **Device ID**: 26 (VIRTIO_FS)
- **Queues**: Queue 0 = hiprio (unused), Queue 1 = request queue
- **Config space** (at MMIO offset 0x100+):
  - Bytes 0-35: Tag string (36 bytes, null-padded)
  - Bytes 36-39: num_request_queues (uint32) = 1

## FUSE Protocol

### Request/Response Flow

```
Guest writes to virtqueue:
  Descriptor chain:
    [0] Readable:  FUSE request header (40 bytes) + request body
    [1] Writable:  FUSE response header (16 bytes) + response body

Host processes:
  1. Concatenate all readable descriptors -> full FUSE request
  2. Parse FUSE header (opcode, nodeid, unique)
  3. Dispatch to handler based on opcode
  4. Build response header + body
  5. Write response across writable descriptors
  6. Return total bytes written
```

### FUSE Request Header (40 bytes)

```go
type fuseInHeader struct {
    Len     uint32  // Total request length (header + body)
    Opcode  uint32  // Operation code (see table below)
    Unique  uint64  // Request ID (echoed in response)
    NodeID  uint64  // Target inode/node
    UID     uint32  // Calling user ID
    GID     uint32  // Calling group ID
    PID     uint32  // Calling process ID
    Padding uint32
}
```

### FUSE Response Header (16 bytes)

```go
type fuseOutHeader struct {
    Len    uint32  // Total response length (header + body)
    Error  int32   // Negative errno (0 = success)
    Unique uint64  // Echoed from request
}
```

## Supported FUSE Operations

| Opcode | Name | Description |
|--------|------|-------------|
| 1 | FUSE_LOOKUP | Look up a directory entry by name |
| 3 | FUSE_GETATTR | Get file attributes (stat) |
| 4 | FUSE_SETATTR | Set file attributes (chmod, truncate, chown) |
| 9 | FUSE_MKDIR | Create directory |
| 10 | FUSE_UNLINK | Delete file |
| 11 | FUSE_RMDIR | Delete directory |
| 12 | FUSE_RENAME | Rename file (v1) |
| 14 | FUSE_OPEN | Open file |
| 15 | FUSE_READ | Read file data |
| 16 | FUSE_WRITE | Write file data |
| 17 | FUSE_STATFS | Get filesystem statistics |
| 25 | FUSE_FLUSH | Flush file buffers |
| 26 | FUSE_INIT | Initialize FUSE connection |
| 27 | FUSE_OPENDIR | Open directory |
| 28 | FUSE_READDIR | Read directory entries |
| 35 | FUSE_CREATE | Create and open file |
| 39 | FUSE_RELEASEDIR | Close directory |
| 44 | FUSE_READDIRPLUS | Read directory with attributes |
| 45 | FUSE_RENAME2 | Rename with flags |

## Node and Handle Management

### Node ID Allocation

```
Node 1 (fuseRootID):  Always maps to hostPath (the shared directory root)
Node 2+:              Allocated on FUSE_LOOKUP, maps to hostPath + relative path
```

```go
func (d *VirtioFSDevice) allocateNode(path string) uint64 {
    d.mu.Lock()
    defer d.mu.Unlock()
    // Check if path already has a node
    for id, p := range d.nodes {
        if p == path { return id }
    }
    id := d.nextNode
    d.nextNode++
    d.nodes[id] = path
    return id
}
```

### File Handle Allocation

```go
func (d *VirtioFSDevice) allocateHandle(f *os.File) uint64 {
    d.mu.Lock()
    defer d.mu.Unlock()
    fh := d.nextHandle
    d.nextHandle++
    d.handles[fh] = f
    return fh
}
```

Handles are released on `FUSE_RELEASE` / `FUSE_RELEASEDIR`.

## Operation Details

### FUSE_INIT (Opcode 26)

Handshake between guest kernel and FUSE server:

```
Request:  fuseInitIn{Major: 7, Minor: N, MaxReadahead, Flags}
Response: fuseInitOut{
    Major: 7, Minor: 38,
    MaxReadahead: 131072,
    MaxWrite: 131072,
    MaxBackground: 16,
    CongestionThreshold: 12,
    TimeGran: 1,
}
```

### FUSE_LOOKUP (Opcode 1)

Resolves a filename within a directory node:

```
Request:  NodeID=parent, Body=filename (null-terminated string)

Processing:
  parentPath = nodes[NodeID]
  fullPath = filepath.Join(parentPath, filename)
  info = os.Lstat(fullPath)
  childNode = allocateNode(fullPath)

Response: fuseEntryOut{
    NodeID: childNode,
    Attr: {mode, size, uid, gid, nlink, atime, mtime, ctime, ...}
}
```

### FUSE_READ (Opcode 15)

```
Request:  fuseReadIn{Fh: fileHandle, Offset: byteOffset, Size: maxBytes}

Processing:
  file = handles[Fh]
  file.ReadAt(buf, Offset)

Response: fuseOutHeader{Len: headerSize + bytesRead} + data
```

### FUSE_WRITE (Opcode 16)

```
Request:  fuseWriteIn{Fh, Offset, Size} + data bytes

Processing:
  if readOnly: return -EACCES
  file = handles[Fh]
  file.WriteAt(data, Offset)

Response: fuseWriteOut{Size: bytesWritten}
```

**Split descriptor handling**: FUSE_WRITE data may span multiple readable descriptors. The handler concatenates all readable descriptor data before extracting the write payload:

```go
// Readable descriptors are concatenated:
// [desc0: FUSE header (40B) + fuseWriteIn (24B)] [desc1: write data]
// The handler reads the full request, then extracts data starting at
// offset 40 (header) + 24 (writeIn body) = 64 bytes into the buffer.
```

### FUSE_READDIR / FUSE_READDIRPLUS (Opcodes 28/44)

```
Request:  fuseReadIn{Fh, Offset, Size}

Processing:
  dir = handles[Fh]
  entries = dir.ReadDir(-1)
  For each entry starting from Offset:
    READDIR:     Pack dirent{ino, off, namelen, type, name}
    READDIRPLUS: Pack dirent + fuseEntryOut{nodeID, attr, ...}

Response: Packed directory entries fitting within Size bytes
```

### FUSE_CREATE (Opcode 35)

```
Request:  fuseCreateIn{Flags, Mode} + filename

Processing:
  if readOnly: return -EACCES
  path = filepath.Join(nodes[NodeID], filename)
  file = os.OpenFile(path, Flags, Mode)
  node = allocateNode(path)
  fh = allocateHandle(file)

Response: fuseEntryOut{NodeID, Attr} + fuseOpenOut{Fh}
```

### FUSE_SETATTR (Opcode 4)

```
Request:  fuseSetAttrIn{Valid, Size, Mode, UID, GID, ...}

Processing (based on Valid bitmask):
  if FATTR_SIZE:  os.Truncate(path, Size)
  if FATTR_MODE:  os.Chmod(path, Mode)
  if FATTR_UID/GID: os.Chown(path, UID, GID)

Response: fuseAttrOut{Attr: updated_stat}
```

## Descriptor Processing

### Split Descriptor Handling

FUSE requests often arrive with the header and body split across multiple descriptors:

```
Typical layout:
  Descriptor 0 (readable):  [FUSE header 40B | request body]
  Descriptor 1 (readable):  [additional request data]  (e.g., WRITE payload)
  Descriptor 2 (writable):  [space for response header]
  Descriptor 3 (writable):  [space for response body]
```

The handler concatenates all readable buffers into a single request buffer, and distributes the response across writable buffers:

```go
func handleFuseRequest(readBufs, writeBufs [][]byte) uint32 {
    // Concatenate readable descriptors into single request
    var reqBuf []byte
    for _, buf := range readBufs {
        reqBuf = append(reqBuf, buf...)
    }

    // Parse header from concatenated buffer
    header := parseFuseHeader(reqBuf[:40])
    body := reqBuf[40:]

    // Process request, build response
    resp := dispatch(header, body)

    // Write response across writable descriptors
    written := 0
    for _, buf := range writeBufs {
        n := copy(buf, resp[written:])
        written += n
        if written >= len(resp) { break }
    }
    return uint32(written)
}
```

## Read-Only Mode

When `readOnly` is true, these operations return `-EACCES`:
- FUSE_SETATTR (mode, size, ownership changes)
- FUSE_WRITE
- FUSE_CREATE
- FUSE_MKDIR
- FUSE_UNLINK
- FUSE_RMDIR
- FUSE_RENAME / FUSE_RENAME2

Read operations (LOOKUP, GETATTR, OPEN, READ, READDIR) work normally.

## Error Handling

FUSE errors are returned as negative errno values in the response header:

| Error | Value | When |
|-------|-------|------|
| `-ENOENT` | -2 | File not found (LOOKUP, GETATTR) |
| `-EIO` | -5 | I/O error (READ, WRITE) |
| `-EACCES` | -13 | Permission denied (write ops on read-only) |
| `-EEXIST` | -17 | File already exists (MKDIR, CREATE with O_EXCL) |
| `-ENOTDIR` | -20 | Not a directory (READDIR on file) |
| `-EINVAL` | -22 | Invalid argument |
| `-ENOSYS` | -38 | Operation not implemented |
| `-ENOTEMPTY` | -39 | Directory not empty (RMDIR) |

## Mount Configuration

VirtioFS mounts are configured via kernel command line:

```
SANDAL_VM_MOUNTS=fs-0=/host/data,fs-1=/home/user/.sandal-vm/images
```

Format: `tag=hostpath` or `tag=hostpath=guestmountpoint`

Guest-side mount:
```bash
mount -t virtiofs fs-0 /mnt/data
```

The guest kernel's virtiofs module communicates with the VirtioFSDevice via the virtqueue, which dispatches FUSE requests to the built-in handler.
