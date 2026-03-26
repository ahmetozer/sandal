# Virtio Device Model

All guest devices use the Virtio specification over MMIO transport. This document covers the transport layer, virtqueue implementation, and each device type.

## Virtio MMIO Transport

**File**: `pkg/vm/kvm/virtio.go`

### Device Address Assignment

Each virtio device occupies a 0x200-byte MMIO region:

```
Device 0: 0x0A000000 - 0x0A0001FF  (IRQ 16)
Device 1: 0x0A000200 - 0x0A0003FF  (IRQ 17)
Device 2: 0x0A000400 - 0x0A0005FF  (IRQ 18)
...
Device N: 0x0A000000 + N*0x200     (IRQ 16+N)
```

Typical device ordering:
```
[0] virtio-console  (device ID 3)
[1] virtio-blk      (device ID 2) - disk image (if present)
[2] virtio-blk      (device ID 2) - ISO image (if present)
[3] virtio-fs       (device ID 26) - first VirtioFS mount
[4] virtio-fs       (device ID 26) - second VirtioFS mount
...
[N] virtio-net      (device ID 1) - always last
```

### MMIO Register Map

| Offset | Name | R/W | Description |
|--------|------|-----|-------------|
| 0x000 | MagicValue | R | Always `0x74726976` ("virt") |
| 0x004 | Version | R | Always `2` (modern virtio) |
| 0x008 | DeviceID | R | Device type (1=net, 2=blk, 3=console, 26=fs) |
| 0x00C | VendorID | R | Always `0x554D4551` ("QEMU") |
| 0x010 | DeviceFeatures | R | Feature bits (selected by DeviceFeaturesSel) |
| 0x014 | DeviceFeaturesSel | W | Feature word selector (0=low 32, 1=high 32) |
| 0x020 | DriverFeatures | W | Features accepted by driver |
| 0x024 | DriverFeaturesSel | W | Driver feature word selector |
| 0x030 | QueueSel | W | Select active queue for configuration |
| 0x034 | QueueNumMax | R | Maximum queue size (256) |
| 0x038 | QueueNum | W | Driver-chosen queue size |
| 0x044 | QueueReady | R/W | Queue initialization complete flag |
| 0x050 | QueueNotify | W | Driver notifies device of new buffers |
| 0x060 | InterruptStatus | R | Pending interrupt flags |
| 0x064 | InterruptACK | W | Guest acknowledges interrupt |
| 0x070 | Status | R/W | Device status register |
| 0x080 | QueueDescLow | W | Descriptor table GPA (low 32) |
| 0x084 | QueueDescHigh | W | Descriptor table GPA (high 32) |
| 0x090 | QueueDriverLow | W | Available ring GPA (low 32) |
| 0x094 | QueueDriverHigh | W | Available ring GPA (high 32) |
| 0x0A0 | QueueDeviceLow | W | Used ring GPA (low 32) |
| 0x0A4 | QueueDeviceHigh | W | Used ring GPA (high 32) |
| 0x0FC | ConfigGeneration | R | Config space change counter |
| 0x100+ | Config | R/W | Device-specific configuration space |

### Device Status Protocol

The guest driver progresses through these status bits:

```
0x00  RESET
0x01  ACKNOWLEDGE     Guest recognizes the device
0x02  DRIVER          Guest knows how to drive the device
0x08  FEATURES_OK     Feature negotiation complete
0x04  DRIVER_OK       Driver fully initialized, device is live
```

When `DRIVER_OK` is set, the device is operational and queues can be used.

## Virtqueue (Split Ring)

### Data Structures

```go
type virtqueue struct {
    num       uint32   // Queue size (max 256)
    ready     bool     // Queue initialized
    descAddr  uint64   // GPA of descriptor table
    drvAddr   uint64   // GPA of available ring (driver area)
    devAddr   uint64   // GPA of used ring (device area)
    lastAvail uint16   // Last processed available ring index
}
```

### Memory Layout (in guest physical memory)

```
Descriptor Table (descAddr):
+-------+-------+-------+-------+
| desc0 | desc1 | desc2 | ...   |  Each descriptor: 16 bytes
+-------+-------+-------+-------+

Available Ring (drvAddr):          Written by guest driver
+-------+-------+-------+-------+
| flags | idx   | ring[0..N-1]  |  idx = next entry to write
+-------+-------+-------+-------+

Used Ring (devAddr):               Written by device (host)
+-------+-------+-------+-------+
| flags | idx   | ring[0..N-1]  |  idx = next entry to write
+-------+-------+-------+-------+
```

### Descriptor Format

```go
type vringDesc struct {
    Addr  uint64  // Guest physical address of data buffer
    Len   uint32  // Buffer length in bytes
    Flags uint16  // VRING_DESC_F_NEXT (1), VRING_DESC_F_WRITE (2)
    Next  uint16  // Index of next descriptor (if NEXT flag set)
}
```

- **NEXT flag (0x1)**: Another descriptor follows in the chain
- **WRITE flag (0x2)**: Buffer is writable by device (used for responses)

### Queue Processing

**File**: `pkg/vm/kvm/virtio.go` - `ProcessAvailRing()`

```
ProcessAvailRing(queueIdx, handler):
  |
  +-- Read avail.idx from guest memory (drvAddr + 2)
  |
  +-- While lastAvail != avail.idx:
  |     |
  |     +-- descIdx = avail.ring[lastAvail % queueSize]  (at drvAddr + 4 + lastAvail*2)
  |     |
  |     +-- Walk descriptor chain from descIdx:
  |     |     readBufs = []  (buffers without WRITE flag)
  |     |     writeBufs = [] (buffers with WRITE flag)
  |     |     while desc has NEXT flag:
  |     |       if desc.Flags & WRITE:
  |     |         writeBufs.append(guestSlice(desc.Addr, desc.Len))
  |     |       else:
  |     |         readBufs.append(guestSlice(desc.Addr, desc.Len))
  |     |       desc = descriptorTable[desc.Next]
  |     |
  |     +-- written = handler(readBufs, writeBufs)
  |     |
  |     +-- Write used ring entry:
  |     |     used.ring[used.idx % queueSize] = {id: descIdx, len: written}
  |     |     used.idx++
  |     |
  |     +-- lastAvail++
  |
  +-- injectIRQ()  // Signal guest that used ring has new entries
```

## Virtio Device Interface

```go
type virtioDevice interface {
    DeviceID() uint32                              // Virtio device type
    Features() uint64                              // Offered feature bits
    ConfigRead(offset uint32, size uint32) uint32  // Read config space
    ConfigWrite(offset uint32, size uint32, val uint32)
    HandleQueue(queueIdx int, dev *virtioMMIODev)  // Process queue buffers
    Tag() string                                   // VirtioFS tag (empty for others)
}
```

## Device: Virtio-Console (ID 3)

**File**: `pkg/vm/kvm/virtio_console.go`

### Purpose
Provides `/dev/hvc0` serial console in the guest, connected to the host's stdin/stdout via pipes.

### Queues
- **Queue 0 (RX)**: Host -> Guest input (device writes to guest buffers)
- **Queue 1 (TX)**: Guest -> Host output (device reads guest buffers)

### Features
```
VIRTIO_CONSOLE_F_SIZE       (1 << 0)  // Console size reported
VIRTIO_CONSOLE_F_MULTIPORT  (1 << 1)  // Multiple ports
VIRTIO_CONSOLE_F_EMERG_WRITE (1 << 2) // Emergency write support
```

### Config Space (offset 0x100+)
```
Offset 0: cols   (uint16) = 80
Offset 2: rows   (uint16) = 25
Offset 4: max_nr_ports (uint32) = 1
```

### Data Flow

```
Host stdin  ──pipe──>  stdinReader
                         |
                    consoleDev.StartRX()
                         |  (goroutine reads from pipe)
                         v
                    RX queue: device writes to guest writable buffers
                         |
                    injectIRQ() -> guest reads /dev/hvc0

Guest writes /dev/hvc0
                         |
                    TX queue: guest puts data in readable buffers
                         |
                    HandleQueue(1): device reads buffers
                         |
                         v
                    stdoutWriter ──pipe──> Host stdout
```

### Emergency Write

When the guest writes to config offset 0 (emergency write register), the character is immediately written to the stdout pipe, bypassing the virtqueue.

## Device: Virtio-Block (ID 2)

**File**: `pkg/vm/kvm/virtio_blk.go`

### Purpose
Provides `/dev/vda`, `/dev/vdb`, etc. block devices backed by host files (disk images, ISOs).

### Queue
- **Queue 0**: Request queue

### Features
```
VIRTIO_BLK_F_SIZE_MAX  (1 << 1)   // Max segment size
VIRTIO_BLK_F_SEG_MAX   (1 << 2)   // Max segments per request
VIRTIO_BLK_F_GEOMETRY  (1 << 4)   // Disk geometry
VIRTIO_BLK_F_RO        (1 << 5)   // Read-only device (for ISOs)
VIRTIO_BLK_F_BLK_SIZE  (1 << 6)   // Block size (512)
VIRTIO_BLK_F_FLUSH     (1 << 9)   // Flush command support
```

### Config Space (offset 0x100+)
```
Offset 0:  capacity (uint64) = file_size / 512 (in sectors)
Offset 8:  size_max (uint32) = 1048576 (1MB)
Offset 12: seg_max  (uint32) = 128
Offset 20: geometry (cylinders, heads, sectors)
Offset 28: blk_size (uint32) = 512
```

### Request Format

A single virtio-blk request uses a descriptor chain:

```
Descriptor 0 (readable):  virtio_blk_req header (16 bytes)
  +--------+----------+--------+
  | type   | reserved | sector |
  | uint32 | uint32   | uint64 |
  +--------+----------+--------+
  type: 0=READ, 1=WRITE, 4=FLUSH

Descriptors 1..N-1 (data):
  For READ:  writable buffers (device fills with disk data)
  For WRITE: readable buffers (device writes to disk)

Descriptor N (writable): status byte (1 byte)
  0 = VIRTIO_BLK_S_OK
  1 = VIRTIO_BLK_S_IOERR
  2 = VIRTIO_BLK_S_UNSUPP
```

### I/O Processing

```go
func (d *VirtioBlkDevice) HandleQueue(queueIdx int, dev *virtioMMIODev) {
    dev.ProcessAvailRing(0, func(readBufs, writeBufs [][]byte) uint32 {
        header := readBufs[0]  // 16-byte request header
        reqType := binary.LittleEndian.Uint32(header[0:4])
        sector := binary.LittleEndian.Uint64(header[8:16])

        switch reqType {
        case 0: // READ
            d.file.Seek(int64(sector*512), io.SeekStart)
            for _, buf := range writeBufs[:len(writeBufs)-1] {
                d.file.Read(buf)
            }
            writeBufs[len(writeBufs)-1][0] = 0  // status = OK

        case 1: // WRITE
            d.file.Seek(int64(sector*512), io.SeekStart)
            for _, buf := range readBufs[1:] {
                d.file.Write(buf)
            }
            writeBufs[0][0] = 0  // status = OK

        case 4: // FLUSH
            d.file.Sync()
            writeBufs[0][0] = 0
        }
    })
}
```

## Device: Virtio-Net (ID 1)

**File**: `pkg/vm/kvm/virtio_net.go`

### Purpose
Provides `eth0` network interface backed by a TAP device on the host's sandal0 bridge.

### Queues
- **Queue 0 (RX)**: Network -> Guest (device writes packets to guest)
- **Queue 1 (TX)**: Guest -> Network (device reads packets from guest)

### Features
```
VIRTIO_NET_F_MAC     (1 << 5)    // Device has MAC address
VIRTIO_NET_F_STATUS  (1 << 16)   // Link status reporting
```

### Config Space (offset 0x100+)
```
Offset 0:  mac[6]     = 52:54:00:xx:xx:01 (derived from VM PID)
Offset 6:  status     = 1 (link up)
Offset 8:  max_virtqueue_pairs = 1
Offset 10: mtu        = 1500
```

### TAP Integration

**File**: `pkg/vm/kvm/tap.go`

```go
func createTAP(name string) (*tapDevice, error) {
    fd, _ := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)

    ifr := ifreq{Flags: IFF_TAP | IFF_NO_PI | IFF_VNET_HDR}
    copy(ifr.Name[:], name)
    ioctl(fd, TUNSETIFF, uintptr(unsafe.Pointer(&ifr)))

    // Enable offloading features
    ioctl(fd, TUNSETOFFLOAD, TUN_F_CSUM|TUN_F_TSO4|TUN_F_TSO6)

    // Set vnet header size (12 bytes for virtio_net_hdr)
    ioctl(fd, TUNSETVNETHDRSZ, 12)

    return &tapDevice{fd: fd, name: name}, nil
}
```

TAP device naming: `sandal<PID % 10000>`, attached to sandal0 bridge via netlink.

### Packet Flow

```
Network packet arrives on sandal0 bridge
  -> TAP device receives packet
  -> StartRX() goroutine reads from TAP fd
  -> ProcessAvailRing(0): copy packet into guest RX writable buffers
  -> injectIRQ() -> guest driver processes packet

Guest sends packet on eth0
  -> TX queue: guest puts packet in readable descriptors
  -> HandleQueue(1): concatenate descriptor data
  -> Write to TAP fd
  -> Packet exits via sandal0 bridge
```

### Vnet Header

Each packet includes a 12-byte `virtio_net_hdr` prefix:

```go
type virtioNetHdr struct {
    Flags      uint8   // NEEDS_CSUM, etc.
    GSOType    uint8   // NONE, TCPV4, TCPV6, UDP
    HdrLen     uint16  // Ethernet + IP + transport header length
    GSOSize    uint16  // Maximum segment size
    CSumStart  uint16  // Offset to start checksumming
    CSumOffset uint16  // Offset to place checksum
}
```

The TAP device is configured with `IFF_VNET_HDR` so the kernel includes/expects this header on each packet.

## Device: Virtio-FS (ID 26)

See [virtiofs-fuse.md](virtiofs-fuse.md) for the full VirtioFS and FUSE implementation.

## Device Registration and MMIO Handling

**File**: `pkg/vm/kvm/virtio.go`

```go
func (dev *virtioMMIODev) handleMMIO(offset uint64, data []byte, size uint32, isWrite bool) {
    if isWrite {
        val := readFromData(data, size)
        switch offset {
        case 0x014: dev.devFeatSel = val
        case 0x020: // Driver features write
        case 0x024: dev.drvFeatSel = val
        case 0x030: dev.queueSel = val
        case 0x038: dev.queues[dev.queueSel].num = val
        case 0x044: dev.queues[dev.queueSel].ready = (val == 1)
        case 0x050: dev.device.HandleQueue(int(val), dev) // Queue notify!
        case 0x064: dev.interruptStat.Store(0) // Interrupt ACK
        case 0x070: dev.status = val
        case 0x080: dev.queues[dev.queueSel].descAddr |= uint64(val) // low
        case 0x084: dev.queues[dev.queueSel].descAddr |= uint64(val)<<32 // high
        // ... similar for driver/device ring addresses
        default:
            if offset >= 0x100 { dev.device.ConfigWrite(uint32(offset-0x100), size, val) }
        }
    } else {
        var val uint32
        switch offset {
        case 0x000: val = 0x74726976 // Magic
        case 0x004: val = 2          // Version
        case 0x008: val = dev.device.DeviceID()
        case 0x010: val = features(dev.devFeatSel)
        case 0x034: val = 256        // Queue num max
        case 0x044: val = boolToU32(dev.queues[dev.queueSel].ready)
        case 0x060: val = dev.interruptStat.Load()
        case 0x070: val = dev.status
        case 0x0FC: val = dev.configGen
        default:
            if offset >= 0x100 { val = dev.device.ConfigRead(uint32(offset-0x100), size) }
        }
        writeToData(data, size, val)
    }
}
```

The critical path is **offset 0x050 (QueueNotify)**: when the guest writes here, it triggers `HandleQueue()` which processes all pending descriptors in the specified queue.
