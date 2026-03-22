//go:build linux

package kvm

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

// VirtioFS implements a virtio-fs device with a built-in FUSE server.
// No external virtiofsd needed — filesystem operations are handled directly in Go.

const (
	// FUSE opcodes (from linux/fuse.h)
	fuseInit        = 26
	fuseLookup      = 1
	fuseForget      = 2
	fuseGetattr     = 3
	fuseSetattr     = 4
	fuseReadlink    = 5
	fuseMkdir       = 9
	fuseUnlink      = 10
	fuseRmdir       = 11
	fuseRename      = 12
	fuseOpen        = 14
	fuseRead        = 15
	fuseWrite       = 16
	fuseStatfs      = 17
	fuseRelease     = 18
	fuseFsync       = 20
	fuseFlush       = 25
	fuseOpendir     = 27
	fuseReaddir     = 28
	fuseReleasedir  = 29
	fuseFsyncdir    = 30
	fuseAccess      = 34
	fuseCreate      = 35
	fuseReaddirplus = 44
	fuseLseek       = 46
	fuseRename2     = 45
	fuseBatchForget = 42
	fuseDestroy     = 38

	// FUSE init flags
	fuseMaxPages = 256

	// FUSE error codes
	fuseErrOK    = 0
	fuseErrNOENT = -2  // -ENOENT
	fuseErrEIO   = -5  // -EIO
	fuseErrACCES = -13 // -EACCES
	fuseErrEXIST = -17 // -EEXIST
	fuseErrNOTDIR = -20 // -ENOTDIR
	fuseErrISDIR  = -21 // -EISDIR
	fuseErrINVAL  = -22 // -EINVAL
	fuseErrNOSYS  = -38 // -ENOSYS

	// FUSE node IDs
	fuseRootID = 1
)

// fuseInHeader is the FUSE request header (40 bytes)
type fuseInHeader struct {
	Len     uint32
	Opcode  uint32
	Unique  uint64
	NodeID  uint64
	UID     uint32
	GID     uint32
	PID     uint32
	Padding uint32
}

// fuseOutHeader is the FUSE response header (16 bytes)
type fuseOutHeader struct {
	Len    uint32
	Error  int32
	Unique uint64
}

// fuseAttr matches struct fuse_attr
type fuseAttr struct {
	Ino       uint64
	Size      uint64
	Blocks    uint64
	Atime     uint64
	Mtime     uint64
	Ctime     uint64
	AtimeNsec uint32
	MtimeNsec uint32
	CtimeNsec uint32
	Mode      uint32
	Nlink     uint32
	UID       uint32
	GID       uint32
	Rdev      uint32
	BlkSize   uint32
	Flags     uint32
}

// fuseEntryOut matches struct fuse_entry_out
type fuseEntryOut struct {
	NodeID         uint64
	Generation     uint64
	EntryValid     uint64
	AttrValid      uint64
	EntryValidNsec uint32
	AttrValidNsec  uint32
	Attr           fuseAttr
}

// fuseAttrOut matches struct fuse_attr_out
type fuseAttrOut struct {
	AttrValid     uint64
	AttrValidNsec uint32
	Dummy         uint32
	Attr          fuseAttr
}

// fuseInitIn matches struct fuse_init_in
type fuseInitIn struct {
	Major        uint32
	Minor        uint32
	MaxReadahead uint32
	Flags        uint32
	Flags2       uint32
}

// fuseInitOut matches struct fuse_init_out
type fuseInitOut struct {
	Major                uint32
	Minor                uint32
	MaxReadahead         uint32
	Flags                uint32
	MaxBackground        uint16
	CongestionThreshold  uint16
	MaxWrite             uint32
	TimeGran             uint32
	MaxPages             uint16
	MapAlignment         uint16
	Flags2               uint32
	MaxStackDepth        uint32
	Unused               [6]uint32
}

// fuseOpenIn matches struct fuse_open_in
type fuseOpenIn struct {
	Flags  uint32
	OpenFlags uint32
}

// fuseOpenOut matches struct fuse_open_out
type fuseOpenOut struct {
	Fh        uint64
	OpenFlags uint32
	BackingID int32
}

// fuseReadIn matches struct fuse_read_in
type fuseReadIn struct {
	Fh      uint64
	Offset  uint64
	Size    uint32
	ReadFlags uint32
	LockOwner uint64
	Flags   uint32
	Padding uint32
}

// fuseWriteIn matches struct fuse_write_in
type fuseWriteIn struct {
	Fh         uint64
	Offset     uint64
	Size       uint32
	WriteFlags uint32
	LockOwner  uint64
	Flags      uint32
	Padding    uint32
}

// fuseWriteOut matches struct fuse_write_out
type fuseWriteOut struct {
	Size    uint32
	Padding uint32
}

// fuseMkdirIn matches struct fuse_mkdir_in
type fuseMkdirIn struct {
	Mode  uint32
	Umask uint32
}

// fuseCreateIn matches struct fuse_create_in
type fuseCreateIn struct {
	Flags   uint32
	Mode    uint32
	Umask   uint32
	OpenFlags uint32
}

// fuseSetAttrIn matches struct fuse_setattr_in
type fuseSetAttrIn struct {
	Valid     uint32
	Padding   uint32
	Fh        uint64
	Size      uint64
	LockOwner uint64
	Atime     uint64
	Mtime     uint64
	Ctime     uint64
	AtimeNsec uint32
	MtimeNsec uint32
	CtimeNsec uint32
	Mode      uint32
	Unused4   uint32
	UID       uint32
	GID       uint32
	Unused5   uint32
}

// fuseStatfsOut matches struct fuse_statfs_out
type fuseStatfsOut struct {
	Blocks  uint64
	Bfree   uint64
	Bavail  uint64
	Files   uint64
	Ffree   uint64
	Bsize   uint32
	Namelen uint32
	Frsize  uint32
	Padding uint32
	Spare   [6]uint32
}

// fuseDirent matches struct fuse_dirent
type fuseDirent struct {
	Ino     uint64
	Off     uint64
	Namelen uint32
	Type    uint32
}

// fuseDirentplus for READDIRPLUS
type fuseDirentplus struct {
	EntryOut fuseEntryOut
	Dirent   fuseDirent
}

// fuseRename2In matches struct fuse_rename2_in
type fuseRename2In struct {
	Newdir  uint64
	Flags   uint32
	Padding uint32
}

// VirtioFSDevice implements a VirtioFS device
type VirtioFSDevice struct {
	tag      string // filesystem tag (matches mount -t virtiofs <tag>)
	hostPath string // host directory to share
	readOnly bool

	mu       sync.Mutex
	nextNode uint64
	nextFh   uint64
	nodes    map[uint64]string  // nodeID -> host path
	fds      map[uint64]*os.File // fh -> open file
}

func NewVirtioFSDevice(tag, hostPath string, readOnly bool) *VirtioFSDevice {
	return &VirtioFSDevice{
		tag:      tag,
		hostPath: hostPath,
		readOnly: readOnly,
		nextNode: fuseRootID + 1,
		nextFh:   1,
		nodes:    map[uint64]string{fuseRootID: hostPath},
		fds:      make(map[uint64]*os.File),
	}
}

func (d *VirtioFSDevice) DeviceID() uint32 { return virtioDevFS }
func (d *VirtioFSDevice) Tag() string       { return d.tag }

func (d *VirtioFSDevice) Features() uint64 {
	return 0 // no special features beyond VERSION_1
}

func (d *VirtioFSDevice) ConfigRead(offset uint32, size uint32) uint32 {
	// VirtioFS config: struct virtio_fs_config { char tag[36]; uint32_t num_request_queues; }
	tag := []byte(d.tag)
	if len(tag) > 36 {
		tag = tag[:36]
	}
	// Pad to 36 bytes
	padded := make([]byte, 40) // 36 + 4
	copy(padded, tag)
	// num_request_queues = 1
	binary.LittleEndian.PutUint32(padded[36:], 1)

	if offset+size > uint32(len(padded)) {
		return 0
	}
	switch size {
	case 1:
		return uint32(padded[offset])
	case 2:
		return uint32(binary.LittleEndian.Uint16(padded[offset:]))
	case 4:
		return binary.LittleEndian.Uint32(padded[offset:])
	}
	return 0
}

func (d *VirtioFSDevice) ConfigWrite(offset uint32, size uint32, val uint32) {
	// Config is read-only
}

func (d *VirtioFSDevice) HandleQueue(queueIdx int, dev *virtioMMIODev) {
	// Queue 0 = hiprio (not used), Queue 1 = request queue
	if queueIdx == 0 {
		// Hiprio queue - just complete any requests
		dev.ProcessAvailRing(queueIdx, func(readBufs, writeBufs [][]byte) uint32 {
			return 0
		})
		return
	}

	dev.ProcessAvailRing(queueIdx, func(readBufs, writeBufs [][]byte) uint32 {
		return d.handleFuseRequest(readBufs, writeBufs)
	})
}

func (d *VirtioFSDevice) handleFuseRequest(readBufs, writeBufs [][]byte) uint32 {
	if len(readBufs) == 0 || len(writeBufs) == 0 {
		return 0
	}

	// First read buffer contains the FUSE in header
	inData := readBufs[0]
	if len(inData) < int(unsafe.Sizeof(fuseInHeader{})) {
		return 0
	}

	hdr := (*fuseInHeader)(unsafe.Pointer(&inData[0]))
	bodyOffset := unsafe.Sizeof(fuseInHeader{})
	inBody := inData[bodyOffset:]

	// Extra read buffers (e.g., write data after fuseWriteIn)
	var extraRead []byte
	if len(readBufs) > 1 {
		extraRead = readBufs[1]
	}

	// First write buffer is where we put the response
	outBuf := writeBufs[0]

	return d.dispatch(hdr, inBody, extraRead, outBuf)
}

func (d *VirtioFSDevice) dispatch(hdr *fuseInHeader, inBody []byte, extraRead []byte, outBuf []byte) uint32 {
	switch hdr.Opcode {
	case fuseInit:
		return d.handleInit(hdr, inBody, outBuf)
	case fuseLookup:
		return d.handleLookup(hdr, inBody, outBuf)
	case fuseGetattr:
		return d.handleGetattr(hdr, outBuf)
	case fuseSetattr:
		return d.handleSetattr(hdr, inBody, outBuf)
	case fuseOpen, fuseOpendir:
		return d.handleOpen(hdr, inBody, outBuf)
	case fuseRead:
		return d.handleRead(hdr, inBody, outBuf)
	case fuseWrite:
		return d.handleWrite(hdr, inBody, extraRead, outBuf)
	case fuseReaddir:
		return d.handleReaddir(hdr, inBody, outBuf, false)
	case fuseReaddirplus:
		return d.handleReaddir(hdr, inBody, outBuf, true)
	case fuseRelease, fuseReleasedir:
		return d.handleRelease(hdr, inBody, outBuf)
	case fuseMkdir:
		return d.handleMkdir(hdr, inBody, outBuf)
	case fuseCreate:
		return d.handleCreate(hdr, inBody, outBuf)
	case fuseUnlink, fuseRmdir:
		return d.handleUnlink(hdr, inBody, outBuf)
	case fuseRename:
		return d.handleRename(hdr, inBody, outBuf)
	case fuseStatfs:
		return d.handleStatfs(hdr, outBuf)
	case fuseFlush, fuseFsync, fuseFsyncdir:
		return d.replyOK(hdr, outBuf)
	case fuseForget:
		// No reply needed for FORGET
		return 0
	case fuseBatchForget:
		return 0
	case fuseAccess:
		return d.replyOK(hdr, outBuf)
	case fuseDestroy:
		return d.replyOK(hdr, outBuf)
	case fuseRename2:
		return d.handleRename2(hdr, inBody, outBuf)
	default:
		return d.replyError(hdr, outBuf, fuseErrNOSYS)
	}
}

func (d *VirtioFSDevice) replyHeader(outBuf []byte, unique uint64, err int32, payloadSize int) uint32 {
	totalLen := uint32(unsafe.Sizeof(fuseOutHeader{})) + uint32(payloadSize)
	if uint32(len(outBuf)) < totalLen {
		totalLen = uint32(len(outBuf))
	}
	out := (*fuseOutHeader)(unsafe.Pointer(&outBuf[0]))
	out.Len = totalLen
	out.Error = err
	out.Unique = unique
	return totalLen
}

func (d *VirtioFSDevice) replyOK(hdr *fuseInHeader, outBuf []byte) uint32 {
	return d.replyHeader(outBuf, hdr.Unique, 0, 0)
}

func (d *VirtioFSDevice) replyError(hdr *fuseInHeader, outBuf []byte, errno int32) uint32 {
	return d.replyHeader(outBuf, hdr.Unique, errno, 0)
}

func (d *VirtioFSDevice) nodePath(nodeID uint64) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.nodes[nodeID]
	return p, ok
}

func (d *VirtioFSDevice) allocNode(path string) uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Check if already allocated
	for id, p := range d.nodes {
		if p == path {
			return id
		}
	}
	id := d.nextNode
	d.nextNode++
	d.nodes[id] = path
	return id
}

func (d *VirtioFSDevice) allocFh(f *os.File) uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	fh := d.nextFh
	d.nextFh++
	d.fds[fh] = f
	return fh
}

func (d *VirtioFSDevice) getFh(fh uint64) *os.File {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fds[fh]
}

func (d *VirtioFSDevice) closeFh(fh uint64) {
	d.mu.Lock()
	f := d.fds[fh]
	delete(d.fds, fh)
	d.mu.Unlock()
	if f != nil {
		f.Close()
	}
}

func (d *VirtioFSDevice) handleInit(hdr *fuseInHeader, inBody []byte, outBuf []byte) uint32 {
	outHdrSize := int(unsafe.Sizeof(fuseOutHeader{}))
	outPayload := int(unsafe.Sizeof(fuseInitOut{}))
	total := outHdrSize + outPayload

	if len(outBuf) < total {
		return d.replyError(hdr, outBuf, fuseErrINVAL)
	}

	d.replyHeader(outBuf, hdr.Unique, 0, outPayload)
	out := (*fuseInitOut)(unsafe.Pointer(&outBuf[outHdrSize]))
	out.Major = 7
	out.Minor = 38
	out.MaxReadahead = 131072
	out.MaxWrite = 131072
	out.MaxPages = fuseMaxPages
	out.MaxBackground = 12
	out.CongestionThreshold = 9
	out.TimeGran = 1

	return uint32(total)
}

func (d *VirtioFSDevice) handleLookup(hdr *fuseInHeader, inBody []byte, outBuf []byte) uint32 {
	parentPath, ok := d.nodePath(hdr.NodeID)
	if !ok {
		return d.replyError(hdr, outBuf, fuseErrNOENT)
	}

	// Name is null-terminated string
	name := cstring(inBody)
	childPath := filepath.Join(parentPath, name)

	var stat syscall.Stat_t
	if err := syscall.Lstat(childPath, &stat); err != nil {
		return d.replyError(hdr, outBuf, errnoToFuse(err))
	}

	nodeID := d.allocNode(childPath)
	outHdrSize := int(unsafe.Sizeof(fuseOutHeader{}))
	outPayload := int(unsafe.Sizeof(fuseEntryOut{}))

	d.replyHeader(outBuf, hdr.Unique, 0, outPayload)
	entry := (*fuseEntryOut)(unsafe.Pointer(&outBuf[outHdrSize]))
	entry.NodeID = nodeID
	entry.Generation = 1
	entry.EntryValid = 1
	entry.AttrValid = 1
	fillAttr(&entry.Attr, &stat)

	return uint32(outHdrSize + outPayload)
}

func (d *VirtioFSDevice) handleGetattr(hdr *fuseInHeader, outBuf []byte) uint32 {
	path, ok := d.nodePath(hdr.NodeID)
	if !ok {
		return d.replyError(hdr, outBuf, fuseErrNOENT)
	}

	var stat syscall.Stat_t
	if err := syscall.Lstat(path, &stat); err != nil {
		return d.replyError(hdr, outBuf, errnoToFuse(err))
	}

	outHdrSize := int(unsafe.Sizeof(fuseOutHeader{}))
	outPayload := int(unsafe.Sizeof(fuseAttrOut{}))

	d.replyHeader(outBuf, hdr.Unique, 0, outPayload)
	attrOut := (*fuseAttrOut)(unsafe.Pointer(&outBuf[outHdrSize]))
	attrOut.AttrValid = 1
	fillAttr(&attrOut.Attr, &stat)

	return uint32(outHdrSize + outPayload)
}

func (d *VirtioFSDevice) handleSetattr(hdr *fuseInHeader, inBody []byte, outBuf []byte) uint32 {
	if d.readOnly {
		return d.replyError(hdr, outBuf, fuseErrACCES)
	}

	path, ok := d.nodePath(hdr.NodeID)
	if !ok {
		return d.replyError(hdr, outBuf, fuseErrNOENT)
	}

	if len(inBody) < int(unsafe.Sizeof(fuseSetAttrIn{})) {
		return d.replyError(hdr, outBuf, fuseErrINVAL)
	}
	in := (*fuseSetAttrIn)(unsafe.Pointer(&inBody[0]))

	if in.Valid&(1<<2) != 0 { // FATTR_SIZE
		if err := os.Truncate(path, int64(in.Size)); err != nil {
			return d.replyError(hdr, outBuf, errnoToFuse(err))
		}
	}
	if in.Valid&(1<<0) != 0 { // FATTR_MODE
		if err := os.Chmod(path, os.FileMode(in.Mode&0o7777)); err != nil {
			return d.replyError(hdr, outBuf, errnoToFuse(err))
		}
	}
	if in.Valid&(1<<3) != 0 || in.Valid&(1<<4) != 0 { // FATTR_UID, FATTR_GID
		uid := int(in.UID)
		gid := int(in.GID)
		if in.Valid&(1<<3) == 0 {
			uid = -1
		}
		if in.Valid&(1<<4) == 0 {
			gid = -1
		}
		syscall.Lchown(path, uid, gid)
	}

	// Return updated attrs
	var stat syscall.Stat_t
	syscall.Lstat(path, &stat)

	outHdrSize := int(unsafe.Sizeof(fuseOutHeader{}))
	outPayload := int(unsafe.Sizeof(fuseAttrOut{}))
	d.replyHeader(outBuf, hdr.Unique, 0, outPayload)
	attrOut := (*fuseAttrOut)(unsafe.Pointer(&outBuf[outHdrSize]))
	attrOut.AttrValid = 1
	fillAttr(&attrOut.Attr, &stat)

	return uint32(outHdrSize + outPayload)
}

func (d *VirtioFSDevice) handleOpen(hdr *fuseInHeader, inBody []byte, outBuf []byte) uint32 {
	path, ok := d.nodePath(hdr.NodeID)
	if !ok {
		return d.replyError(hdr, outBuf, fuseErrNOENT)
	}

	flags := os.O_RDONLY
	if len(inBody) >= int(unsafe.Sizeof(fuseOpenIn{})) {
		in := (*fuseOpenIn)(unsafe.Pointer(&inBody[0]))
		flags = int(in.Flags) & (os.O_RDONLY | os.O_WRONLY | os.O_RDWR | os.O_APPEND | os.O_TRUNC)
	}
	if d.readOnly {
		flags = os.O_RDONLY
	}

	f, err := os.OpenFile(path, flags, 0)
	if err != nil {
		return d.replyError(hdr, outBuf, errnoToFuse(err))
	}

	fh := d.allocFh(f)
	outHdrSize := int(unsafe.Sizeof(fuseOutHeader{}))
	outPayload := int(unsafe.Sizeof(fuseOpenOut{}))

	d.replyHeader(outBuf, hdr.Unique, 0, outPayload)
	out := (*fuseOpenOut)(unsafe.Pointer(&outBuf[outHdrSize]))
	out.Fh = fh

	return uint32(outHdrSize + outPayload)
}

func (d *VirtioFSDevice) handleRead(hdr *fuseInHeader, inBody []byte, outBuf []byte) uint32 {
	if len(inBody) < int(unsafe.Sizeof(fuseReadIn{})) {
		return d.replyError(hdr, outBuf, fuseErrINVAL)
	}
	in := (*fuseReadIn)(unsafe.Pointer(&inBody[0]))

	f := d.getFh(in.Fh)
	if f == nil {
		return d.replyError(hdr, outBuf, fuseErrINVAL)
	}

	outHdrSize := int(unsafe.Sizeof(fuseOutHeader{}))
	maxRead := len(outBuf) - outHdrSize
	if int(in.Size) < maxRead {
		maxRead = int(in.Size)
	}

	n, err := f.ReadAt(outBuf[outHdrSize:outHdrSize+maxRead], int64(in.Offset))
	if n == 0 && err != nil {
		return d.replyHeader(outBuf, hdr.Unique, 0, 0) // EOF
	}

	return d.replyHeader(outBuf, hdr.Unique, 0, n)
}

func (d *VirtioFSDevice) handleWrite(hdr *fuseInHeader, inBody []byte, extraRead []byte, outBuf []byte) uint32 {
	if d.readOnly {
		return d.replyError(hdr, outBuf, fuseErrACCES)
	}
	if len(inBody) < int(unsafe.Sizeof(fuseWriteIn{})) {
		return d.replyError(hdr, outBuf, fuseErrINVAL)
	}
	in := (*fuseWriteIn)(unsafe.Pointer(&inBody[0]))

	f := d.getFh(in.Fh)
	if f == nil {
		return d.replyError(hdr, outBuf, fuseErrINVAL)
	}

	// Write data follows the fuseWriteIn header or in extra read buffer
	writeData := inBody[unsafe.Sizeof(fuseWriteIn{}):]
	if len(writeData) == 0 && len(extraRead) > 0 {
		writeData = extraRead
	}
	if uint32(len(writeData)) > in.Size {
		writeData = writeData[:in.Size]
	}

	n, err := f.WriteAt(writeData, int64(in.Offset))
	if err != nil && n == 0 {
		return d.replyError(hdr, outBuf, errnoToFuse(err))
	}

	outHdrSize := int(unsafe.Sizeof(fuseOutHeader{}))
	outPayload := int(unsafe.Sizeof(fuseWriteOut{}))
	d.replyHeader(outBuf, hdr.Unique, 0, outPayload)
	out := (*fuseWriteOut)(unsafe.Pointer(&outBuf[outHdrSize]))
	out.Size = uint32(n)

	return uint32(outHdrSize + outPayload)
}

func (d *VirtioFSDevice) handleReaddir(hdr *fuseInHeader, inBody []byte, outBuf []byte, plus bool) uint32 {
	if len(inBody) < int(unsafe.Sizeof(fuseReadIn{})) {
		return d.replyError(hdr, outBuf, fuseErrINVAL)
	}
	in := (*fuseReadIn)(unsafe.Pointer(&inBody[0]))

	f := d.getFh(in.Fh)
	if f == nil {
		return d.replyError(hdr, outBuf, fuseErrINVAL)
	}

	entries, err := f.ReadDir(-1)
	if err != nil {
		return d.replyError(hdr, outBuf, errnoToFuse(err))
	}

	outHdrSize := int(unsafe.Sizeof(fuseOutHeader{}))
	maxSize := int(in.Size)
	if maxSize > len(outBuf)-outHdrSize {
		maxSize = len(outBuf) - outHdrSize
	}

	buf := outBuf[outHdrSize:]
	offset := 0
	dirPath, _ := d.nodePath(hdr.NodeID)

	for i, entry := range entries {
		if uint64(i) < in.Offset {
			continue
		}

		name := []byte(entry.Name())
		nameLen := len(name)

		if plus {
			entrySize := int(unsafe.Sizeof(fuseDirentplus{})) + nameLen
			entrySize = (entrySize + 7) &^ 7 // 8-byte align

			if offset+entrySize > maxSize {
				break
			}

			childPath := filepath.Join(dirPath, entry.Name())
			var stat syscall.Stat_t
			syscall.Lstat(childPath, &stat)
			nodeID := d.allocNode(childPath)

			dp := (*fuseDirentplus)(unsafe.Pointer(&buf[offset]))
			dp.EntryOut.NodeID = nodeID
			dp.EntryOut.Generation = 1
			dp.EntryOut.EntryValid = 1
			dp.EntryOut.AttrValid = 1
			fillAttr(&dp.EntryOut.Attr, &stat)
			dp.Dirent.Ino = stat.Ino
			dp.Dirent.Off = uint64(i + 1)
			dp.Dirent.Namelen = uint32(nameLen)
			dp.Dirent.Type = dtType(stat.Mode)
			copy(buf[offset+int(unsafe.Sizeof(fuseDirentplus{})):], name)
			offset += entrySize
		} else {
			entrySize := int(unsafe.Sizeof(fuseDirent{})) + nameLen
			entrySize = (entrySize + 7) &^ 7

			if offset+entrySize > maxSize {
				break
			}

			var stat syscall.Stat_t
			childPath := filepath.Join(dirPath, entry.Name())
			syscall.Lstat(childPath, &stat)

			de := (*fuseDirent)(unsafe.Pointer(&buf[offset]))
			de.Ino = stat.Ino
			de.Off = uint64(i + 1)
			de.Namelen = uint32(nameLen)
			de.Type = dtType(stat.Mode)
			copy(buf[offset+int(unsafe.Sizeof(fuseDirent{})):], name)
			offset += entrySize
		}
	}

	return d.replyHeader(outBuf, hdr.Unique, 0, offset)
}

func (d *VirtioFSDevice) handleRelease(hdr *fuseInHeader, inBody []byte, outBuf []byte) uint32 {
	if len(inBody) >= 8 {
		fh := binary.LittleEndian.Uint64(inBody[:8])
		d.closeFh(fh)
	}
	return d.replyOK(hdr, outBuf)
}

func (d *VirtioFSDevice) handleMkdir(hdr *fuseInHeader, inBody []byte, outBuf []byte) uint32 {
	if d.readOnly {
		return d.replyError(hdr, outBuf, fuseErrACCES)
	}
	parentPath, ok := d.nodePath(hdr.NodeID)
	if !ok {
		return d.replyError(hdr, outBuf, fuseErrNOENT)
	}
	if len(inBody) < int(unsafe.Sizeof(fuseMkdirIn{})) {
		return d.replyError(hdr, outBuf, fuseErrINVAL)
	}
	in := (*fuseMkdirIn)(unsafe.Pointer(&inBody[0]))
	name := cstring(inBody[unsafe.Sizeof(fuseMkdirIn{}):])
	childPath := filepath.Join(parentPath, name)

	if err := os.Mkdir(childPath, os.FileMode(in.Mode&0o7777)); err != nil {
		return d.replyError(hdr, outBuf, errnoToFuse(err))
	}

	var stat syscall.Stat_t
	syscall.Lstat(childPath, &stat)
	nodeID := d.allocNode(childPath)

	outHdrSize := int(unsafe.Sizeof(fuseOutHeader{}))
	outPayload := int(unsafe.Sizeof(fuseEntryOut{}))
	d.replyHeader(outBuf, hdr.Unique, 0, outPayload)
	entry := (*fuseEntryOut)(unsafe.Pointer(&outBuf[outHdrSize]))
	entry.NodeID = nodeID
	entry.Generation = 1
	entry.EntryValid = 1
	entry.AttrValid = 1
	fillAttr(&entry.Attr, &stat)

	return uint32(outHdrSize + outPayload)
}

func (d *VirtioFSDevice) handleCreate(hdr *fuseInHeader, inBody []byte, outBuf []byte) uint32 {
	if d.readOnly {
		return d.replyError(hdr, outBuf, fuseErrACCES)
	}
	parentPath, ok := d.nodePath(hdr.NodeID)
	if !ok {
		return d.replyError(hdr, outBuf, fuseErrNOENT)
	}
	if len(inBody) < int(unsafe.Sizeof(fuseCreateIn{})) {
		return d.replyError(hdr, outBuf, fuseErrINVAL)
	}
	in := (*fuseCreateIn)(unsafe.Pointer(&inBody[0]))
	name := cstring(inBody[unsafe.Sizeof(fuseCreateIn{}):])
	childPath := filepath.Join(parentPath, name)

	flags := int(in.Flags) | os.O_CREATE
	f, err := os.OpenFile(childPath, flags, os.FileMode(in.Mode&0o7777))
	if err != nil {
		return d.replyError(hdr, outBuf, errnoToFuse(err))
	}

	fh := d.allocFh(f)
	var stat syscall.Stat_t
	syscall.Lstat(childPath, &stat)
	nodeID := d.allocNode(childPath)

	outHdrSize := int(unsafe.Sizeof(fuseOutHeader{}))
	entrySize := int(unsafe.Sizeof(fuseEntryOut{}))
	openSize := int(unsafe.Sizeof(fuseOpenOut{}))

	d.replyHeader(outBuf, hdr.Unique, 0, entrySize+openSize)
	entry := (*fuseEntryOut)(unsafe.Pointer(&outBuf[outHdrSize]))
	entry.NodeID = nodeID
	entry.Generation = 1
	entry.EntryValid = 1
	entry.AttrValid = 1
	fillAttr(&entry.Attr, &stat)

	openOut := (*fuseOpenOut)(unsafe.Pointer(&outBuf[outHdrSize+entrySize]))
	openOut.Fh = fh

	return uint32(outHdrSize + entrySize + openSize)
}

func (d *VirtioFSDevice) handleUnlink(hdr *fuseInHeader, inBody []byte, outBuf []byte) uint32 {
	if d.readOnly {
		return d.replyError(hdr, outBuf, fuseErrACCES)
	}
	parentPath, ok := d.nodePath(hdr.NodeID)
	if !ok {
		return d.replyError(hdr, outBuf, fuseErrNOENT)
	}
	name := cstring(inBody)
	childPath := filepath.Join(parentPath, name)

	var err error
	if hdr.Opcode == fuseRmdir {
		err = os.Remove(childPath)
	} else {
		err = os.Remove(childPath)
	}
	if err != nil {
		return d.replyError(hdr, outBuf, errnoToFuse(err))
	}
	return d.replyOK(hdr, outBuf)
}

func (d *VirtioFSDevice) handleRename(hdr *fuseInHeader, inBody []byte, outBuf []byte) uint32 {
	if d.readOnly {
		return d.replyError(hdr, outBuf, fuseErrACCES)
	}
	if len(inBody) < 8 {
		return d.replyError(hdr, outBuf, fuseErrINVAL)
	}
	newDir := binary.LittleEndian.Uint64(inBody[:8])
	names := inBody[8:]
	oldName := cstring(names)
	newName := cstring(names[len(oldName)+1:])

	oldParent, ok := d.nodePath(hdr.NodeID)
	if !ok {
		return d.replyError(hdr, outBuf, fuseErrNOENT)
	}
	newParent, ok := d.nodePath(newDir)
	if !ok {
		return d.replyError(hdr, outBuf, fuseErrNOENT)
	}

	oldPath := filepath.Join(oldParent, oldName)
	newPath := filepath.Join(newParent, newName)

	if err := os.Rename(oldPath, newPath); err != nil {
		return d.replyError(hdr, outBuf, errnoToFuse(err))
	}
	return d.replyOK(hdr, outBuf)
}

func (d *VirtioFSDevice) handleRename2(hdr *fuseInHeader, inBody []byte, outBuf []byte) uint32 {
	if d.readOnly {
		return d.replyError(hdr, outBuf, fuseErrACCES)
	}
	if len(inBody) < int(unsafe.Sizeof(fuseRename2In{})) {
		return d.replyError(hdr, outBuf, fuseErrINVAL)
	}
	in := (*fuseRename2In)(unsafe.Pointer(&inBody[0]))
	names := inBody[unsafe.Sizeof(fuseRename2In{}):]
	oldName := cstring(names)
	newName := cstring(names[len(oldName)+1:])

	oldParent, ok := d.nodePath(hdr.NodeID)
	if !ok {
		return d.replyError(hdr, outBuf, fuseErrNOENT)
	}
	newParent, ok := d.nodePath(in.Newdir)
	if !ok {
		return d.replyError(hdr, outBuf, fuseErrNOENT)
	}

	oldPath := filepath.Join(oldParent, oldName)
	newPath := filepath.Join(newParent, newName)

	if err := os.Rename(oldPath, newPath); err != nil {
		return d.replyError(hdr, outBuf, errnoToFuse(err))
	}
	return d.replyOK(hdr, outBuf)
}

func (d *VirtioFSDevice) handleStatfs(hdr *fuseInHeader, outBuf []byte) uint32 {
	path, ok := d.nodePath(hdr.NodeID)
	if !ok {
		path = d.hostPath
	}

	var sfs syscall.Statfs_t
	if err := syscall.Statfs(path, &sfs); err != nil {
		return d.replyError(hdr, outBuf, errnoToFuse(err))
	}

	outHdrSize := int(unsafe.Sizeof(fuseOutHeader{}))
	outPayload := int(unsafe.Sizeof(fuseStatfsOut{}))
	d.replyHeader(outBuf, hdr.Unique, 0, outPayload)
	out := (*fuseStatfsOut)(unsafe.Pointer(&outBuf[outHdrSize]))
	out.Blocks = sfs.Blocks
	out.Bfree = sfs.Bfree
	out.Bavail = sfs.Bavail
	out.Files = sfs.Files
	out.Ffree = sfs.Ffree
	out.Bsize = uint32(sfs.Bsize)
	out.Namelen = uint32(sfs.Namelen)
	out.Frsize = uint32(sfs.Frsize)

	return uint32(outHdrSize + outPayload)
}

// Helper functions

func fillAttr(attr *fuseAttr, stat *syscall.Stat_t) {
	attr.Ino = stat.Ino
	attr.Size = uint64(stat.Size)
	attr.Blocks = uint64(stat.Blocks)
	attr.Atime = uint64(stat.Atim.Sec)
	attr.AtimeNsec = uint32(stat.Atim.Nsec)
	attr.Mtime = uint64(stat.Mtim.Sec)
	attr.MtimeNsec = uint32(stat.Mtim.Nsec)
	attr.Ctime = uint64(stat.Ctim.Sec)
	attr.CtimeNsec = uint32(stat.Ctim.Nsec)
	attr.Mode = stat.Mode
	attr.Nlink = uint32(stat.Nlink)
	attr.UID = stat.Uid
	attr.GID = stat.Gid
	attr.Rdev = uint32(stat.Rdev)
	attr.BlkSize = uint32(stat.Blksize)
}

func cstring(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

func dtType(mode uint32) uint32 {
	return (mode >> 12) & 15
}

func errnoToFuse(err error) int32 {
	if e, ok := err.(syscall.Errno); ok {
		return -int32(e)
	}
	if os.IsNotExist(err) {
		return fuseErrNOENT
	}
	if os.IsPermission(err) {
		return fuseErrACCES
	}
	if os.IsExist(err) {
		return fuseErrEXIST
	}
	return fuseErrEIO
}

// Ensure fmt is used
var _ = fmt.Sprintf
