package squashfs

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultBlockSize  = 131072 // 128KB
	metadataBlockSize = 8192   // 8KB
	metadataUncompBit = 1 << 15
	dataUncompBit     = 1 << 24
)

// Compression IDs
const (
	CompGzip SquashfsCompression = 1
)

// Inode types (squashfs 4.0)
const (
	sqfsTypeDir     = 1
	sqfsTypeFile    = 2
	sqfsTypeSymlink = 3
	sqfsTypeLDir    = 8 // Extended directory inode (file_size is uint32)
)

// Superblock flags
const (
	flagNoXattrs   = 0x0200
	flagDuplicates = 0x0040
)

// Writer creates a squashfs filesystem image.
type Writer struct {
	out       io.WriteSeeker
	blockSize uint32
	comp      SquashfsCompression
	mkfsTime  time.Time

	nextInode uint32
	idTable   []uint32
	idMap     map[uint32]uint16

	// Raw metadata buffers
	inodeRaw bytes.Buffer
	dirRaw   bytes.Buffer

	// inodeNum -> byte offset in inodeRaw
	inodePos map[uint32]int

	// Pending directory entries (raw byte positions for deferred fixup)
	dirEntryFixups []dirEntryFixup
	dirInodeFixups []dirInodeFixup

	// Fragments
	fragments []fragmentEntry
	fragBuf   bytes.Buffer

	// Reusable compression state
	compBuf bytes.Buffer // reusable output buffer for compress()
	compW   *zlib.Writer // reusable zlib writer

	// Reusable file-read buffer (blockSize bytes), avoids per-file allocation.
	fileBuf []byte

	// Optional path filter: receives path relative to rootPath (e.g. "/etc/motd").
	// Returns true to include the entry, false to skip it.
	pathFilter func(relPath string, isDir bool) bool

	// Optional progress callback invoked after each file's data is written.
	progressFn func(bytesWritten int64)

	// listDir returns the child names of dirPath, sorted ascending.
	// Defaults to os.ReadDir. CreateFromPaths overrides this to use a
	// caller-supplied path list so a short os.ReadDir on overlayfs cannot
	// silently drop entries.
	listDir func(dirPath string) ([]string, error)

	dataPos int64
}

type fragmentEntry struct {
	start uint64
	size  uint32
}

// dirEntryFixup: patch a dir entry header's "start" field (inode block offset)
type dirEntryFixup struct {
	dirRawOffset   int    // position in dirRaw of the u32 "start" field in dir header
	firstInodeNum  uint32 // inode number of first child (to compute block offset)
}

// dirInodeFixup: patch a dir inode's block_index and block_offset fields
type dirInodeFixup struct {
	inodeRawOffset int // position in inodeRaw of the u32 "block_index" field
	dirRawPos      int // raw byte position in dirRaw where entries start
}

type inodeInfo struct {
	inodeNum  uint32
	inodeType uint16
	name      string
}

type WriterOption func(*Writer)

func WithBlockSize(size uint32) WriterOption {
	return func(w *Writer) { w.blockSize = size }
}

func WithCompression(c SquashfsCompression) WriterOption {
	return func(w *Writer) { w.comp = c }
}

func WithMkfsTime(t time.Time) WriterOption {
	return func(w *Writer) { w.mkfsTime = t }
}

// WithProgressFunc sets a callback invoked after each file's data is written.
// bytesWritten is the cumulative data bytes written so far.
func WithProgressFunc(fn func(bytesWritten int64)) WriterOption {
	return func(w *Writer) { w.progressFn = fn }
}

// WithPathFilter sets a filter function that controls which paths are included.
// The filter receives the path relative to the root (e.g. "/etc/motd", "/bin")
// and whether the entry is a directory. Return true to include, false to skip.
func WithPathFilter(fn func(relPath string, isDir bool) bool) WriterOption {
	return func(w *Writer) { w.pathFilter = fn }
}

// NewWriter creates a new squashfs writer.
func NewWriter(out io.WriteSeeker, opts ...WriterOption) (*Writer, error) {
	w := &Writer{
		out:       out,
		blockSize: defaultBlockSize,
		comp:      CompGzip,
		mkfsTime:  time.Now(),
		nextInode: 1,
		idMap:     make(map[uint32]uint16),
		inodePos:  make(map[uint32]int),
	}
	for _, opt := range opts {
		opt(w)
	}
	w.dataPos = int64(SquashfsHeaderSize)
	if _, err := w.out.Seek(w.dataPos, io.SeekStart); err != nil {
		return nil, err
	}
	zw, err := zlib.NewWriterLevel(&w.compBuf, zlib.DefaultCompression)
	if err != nil {
		return nil, err
	}
	w.compW = zw
	w.fileBuf = make([]byte, w.blockSize)
	return w, nil
}

func (w *Writer) allocInode() uint32 {
	n := w.nextInode
	w.nextInode++
	return n
}

func (w *Writer) lookupID(id uint32) uint16 {
	if idx, ok := w.idMap[id]; ok {
		return idx
	}
	idx := uint16(len(w.idTable))
	w.idTable = append(w.idTable, id)
	w.idMap[id] = idx
	return idx
}

// compressTo compresses data into w.compBuf.  The result is only valid
// until the next call to compressTo.
func (w *Writer) compressTo(data []byte) error {
	w.compBuf.Reset()
	w.compW.Reset(&w.compBuf)
	if _, err := w.compW.Write(data); err != nil {
		return err
	}
	return w.compW.Close()
}

// writeDataBlock compresses and writes a data block to disk.
func (w *Writer) writeDataBlock(data []byte) (uint32, error) {
	if err := w.compressTo(data); err != nil {
		return 0, err
	}
	var toWrite []byte
	var size uint32
	if w.compBuf.Len() < len(data) {
		toWrite = w.compBuf.Bytes()
		size = uint32(w.compBuf.Len())
	} else {
		toWrite = data
		size = uint32(len(data)) | uint32(dataUncompBit)
	}
	if _, err := w.out.Write(toWrite); err != nil {
		return 0, err
	}
	w.dataPos += int64(len(toWrite))
	return size, nil
}

func (w *Writer) addFragment(data []byte) (uint32, uint32, error) {
	if w.fragBuf.Len()+len(data) > int(w.blockSize) && w.fragBuf.Len() > 0 {
		if err := w.flushFragBlock(); err != nil {
			return 0, 0, err
		}
	}
	fragIdx := uint32(len(w.fragments))
	offset := uint32(w.fragBuf.Len())
	w.fragBuf.Write(data)
	return fragIdx, offset, nil
}

func (w *Writer) flushFragBlock() error {
	if w.fragBuf.Len() == 0 {
		return nil
	}
	data := w.fragBuf.Bytes()
	startPos := uint64(w.dataPos)

	if err := w.compressTo(data); err != nil {
		return err
	}
	var toWrite []byte
	var size uint32
	if w.compBuf.Len() < len(data) {
		toWrite = w.compBuf.Bytes()
		size = uint32(w.compBuf.Len())
	} else {
		toWrite = data
		size = uint32(len(data)) | uint32(dataUncompBit)
	}
	if _, err := w.out.Write(toWrite); err != nil {
		return err
	}
	w.dataPos += int64(len(toWrite))
	w.fragments = append(w.fragments, fragmentEntry{start: startPos, size: size})
	w.fragBuf.Reset()
	return nil
}

func (w *Writer) writeFileDataStreamed(f *os.File, fileSize int64) (uint32, []uint32, uint32, uint32, error) {
	startBlock := uint32(w.dataPos)
	var blockSizes []uint32
	fragIdx := uint32(0xFFFFFFFF)
	fragOff := uint32(0)

	bs := int(w.blockSize)
	buf := w.fileBuf[:bs]
	r := bufio.NewReaderSize(f, bs)

	remaining := fileSize
	for remaining >= int64(bs) {
		if _, err := io.ReadFull(r, buf); err != nil {
			return 0, nil, 0, 0, err
		}
		size, err := w.writeDataBlock(buf)
		if err != nil {
			return 0, nil, 0, 0, err
		}
		blockSizes = append(blockSizes, size)
		remaining -= int64(bs)
	}

	if remaining > 0 {
		tail := buf[:remaining]
		if _, err := io.ReadFull(r, tail); err != nil {
			return 0, nil, 0, 0, err
		}
		var err error
		fragIdx, fragOff, err = w.addFragment(tail)
		if err != nil {
			return 0, nil, 0, 0, err
		}
	}

	return startBlock, blockSizes, fragIdx, fragOff, nil
}

func (w *Writer) appendInode(inodeNum uint32, data []byte) {
	w.inodePos[inodeNum] = w.inodeRaw.Len()
	w.inodeRaw.Write(data)
}

// metadataCache holds pre-compressed metadata blocks to avoid redundant compression.
type metadataCache struct {
	offsets []int64  // byte offset of each block relative to table start
	blocks  [][]byte // compressed (or raw if incompressible) data per block
	headers []uint16 // metadata header per block
}

// compressMetadataBlocks splits raw metadata into 8KB chunks and compresses each one.
// When fixedSize is true, blocks are stored uncompressed so that byte offsets
// are deterministic regardless of content.  This avoids a circular dependency
// when inode and directory tables reference each other's compressed positions.
func (w *Writer) compressMetadataBlocks(raw []byte, fixedSize bool) (*metadataCache, error) {
	mc := &metadataCache{}
	pos := int64(0)
	for i := 0; i < len(raw); i += metadataBlockSize {
		mc.offsets = append(mc.offsets, pos)
		end := i + metadataBlockSize
		if end > len(raw) {
			end = len(raw)
		}
		chunk := raw[i:end]

		if fixedSize {
			stored := make([]byte, len(chunk))
			copy(stored, chunk)
			mc.blocks = append(mc.blocks, stored)
			mc.headers = append(mc.headers, uint16(len(chunk))|metadataUncompBit)
			pos += 2 + int64(len(chunk))
			continue
		}

		if err := w.compressTo(chunk); err != nil {
			return nil, err
		}
		if w.compBuf.Len() < len(chunk) {
			compressed := make([]byte, w.compBuf.Len())
			copy(compressed, w.compBuf.Bytes())
			mc.blocks = append(mc.blocks, compressed)
			mc.headers = append(mc.headers, uint16(len(compressed)))
			pos += 2 + int64(len(compressed))
		} else {
			stored := make([]byte, len(chunk))
			copy(stored, chunk)
			mc.blocks = append(mc.blocks, stored)
			mc.headers = append(mc.headers, uint16(len(chunk))|metadataUncompBit)
			pos += 2 + int64(len(chunk))
		}
	}
	return mc, nil
}

// writeMetadataCached writes pre-compressed metadata blocks to disk. Returns start offset.
func (w *Writer) writeMetadataCached(mc *metadataCache) (int64, error) {
	startOff := w.dataPos
	for i, block := range mc.blocks {
		if err := binary.Write(w.out, binary.LittleEndian, mc.headers[i]); err != nil {
			return 0, err
		}
		if _, err := w.out.Write(block); err != nil {
			return 0, err
		}
		w.dataPos += 2 + int64(len(block))
	}
	return startOff, nil
}

func inodeRef(rawPos int, blockOffsets []int64) uint64 {
	blockIdx := rawPos / metadataBlockSize
	offsetInBlock := rawPos % metadataBlockSize
	return uint64(blockOffsets[blockIdx])<<16 | uint64(offsetInBlock)
}

// writeMetadataBlocksTracked writes metadata blocks and returns each block's disk position.
func (w *Writer) writeMetadataBlocksTracked(raw []byte) ([]int64, error) {
	var positions []int64
	for i := 0; i < len(raw); i += metadataBlockSize {
		positions = append(positions, w.dataPos)
		end := i + metadataBlockSize
		if end > len(raw) {
			end = len(raw)
		}
		chunk := raw[i:end]
		if err := w.compressTo(chunk); err != nil {
			return nil, err
		}
		var header uint16
		var toWrite []byte
		if w.compBuf.Len() < len(chunk) {
			header = uint16(w.compBuf.Len())
			toWrite = w.compBuf.Bytes()
		} else {
			header = uint16(len(chunk)) | metadataUncompBit
			toWrite = chunk
		}
		if err := binary.Write(w.out, binary.LittleEndian, header); err != nil {
			return nil, err
		}
		if _, err := w.out.Write(toWrite); err != nil {
			return nil, err
		}
		w.dataPos += 2 + int64(len(toWrite))
	}
	return positions, nil
}

// CreateFromDir creates a squashfs image from a directory tree.
func (w *Writer) CreateFromDir(rootPath string) error {
	w.listDir = func(dirPath string) ([]string, error) {
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			return nil, err
		}
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		return names, nil
	}
	return w.build(rootPath)
}

// CreateFromPaths creates a squashfs image containing exactly the entries
// listed in paths (relative to rootPath). Intermediate directories implied
// by the list are included automatically. Unlike CreateFromDir, this API
// never calls os.ReadDir, which on overlayfs can silently truncate — it
// trusts the caller-supplied list as ground truth.
func (w *Writer) CreateFromPaths(rootPath string, paths []string) error {
	children := make(map[string]map[string]struct{})
	add := func(parent, name string) {
		if children[parent] == nil {
			children[parent] = map[string]struct{}{}
		}
		children[parent][name] = struct{}{}
	}
	rootClean := filepath.Clean(rootPath)
	for _, rel := range paths {
		rel = filepath.Clean(rel)
		if rel == "." || rel == "/" {
			continue
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		cur := rootClean
		for _, part := range parts {
			if part == "" {
				continue
			}
			add(cur, part)
			cur = filepath.Join(cur, part)
		}
	}
	w.listDir = func(dirPath string) ([]string, error) {
		set := children[filepath.Clean(dirPath)]
		names := make([]string, 0, len(set))
		for n := range set {
			names = append(names, n)
		}
		return names, nil
	}
	return w.build(rootPath)
}

// build is the shared tail of CreateFromDir/CreateFromPaths that runs once
// w.listDir has been configured.
func (w *Writer) build(rootPath string) error {
	// Phase 1: Walk tree bottom-up, write file data, build raw inodes and dir entries
	// All block references are stored as PLACEHOLDERS and fixed up later.
	_, err := w.processDir(rootPath, rootPath)
	if err != nil {
		return fmt.Errorf("walking directory tree: %w", err)
	}
	if err := w.flushFragBlock(); err != nil {
		return fmt.Errorf("flushing fragments: %w", err)
	}

	inodeData := w.inodeRaw.Bytes()
	dirData := w.dirRaw.Bytes()

	// Phase 2: Resolve the circular dependency between inode and directory
	// table positions.  Directory entries store byte offsets into the
	// inode table, and directory inodes store byte offsets into the
	// directory table.
	//
	// Both tables are stored uncompressed so that block byte offsets
	// are deterministic (block N starts at N*(8192+2)) regardless of
	// the values embedded in them.  This lets us patch cross-references
	// in a single pass without any convergence loop.
	inodeTableStart := w.dataPos

	var err2 error

	// With fixed-size (uncompressed) blocks, offsets are content-independent,
	// so one pass is sufficient: compute inode offsets → patch dir entries →
	// compute dir offsets → patch dir inodes → recompute inode offsets (unchanged).
	inodeCache, err2 := w.compressMetadataBlocks(inodeData, true)
	if err2 != nil {
		return fmt.Errorf("computing inode block offsets: %w", err2)
	}

	// Patch dir entry "start" fields with inode block offsets.
	for _, fix := range w.dirEntryFixups {
		rawPos := w.inodePos[fix.firstInodeNum]
		blockIdx := rawPos / metadataBlockSize
		binary.LittleEndian.PutUint32(dirData[fix.dirRawOffset:], uint32(inodeCache.offsets[blockIdx]))
	}

	dirCache, err2 := w.compressMetadataBlocks(dirData, true)
	if err2 != nil {
		return fmt.Errorf("computing dir block offsets: %w", err2)
	}

	// Patch dir inode block_index and block_offset with dir block offsets.
	for _, fix := range w.dirInodeFixups {
		blockIdx := fix.dirRawPos / metadataBlockSize
		binary.LittleEndian.PutUint32(inodeData[fix.inodeRawOffset:], uint32(dirCache.offsets[blockIdx]))
		binary.LittleEndian.PutUint16(inodeData[fix.inodeRawOffset+10:], uint16(fix.dirRawPos%metadataBlockSize))
	}

	// Re-split the patched inode data (offsets unchanged since blocks are fixed-size).
	inodeCache, err2 = w.compressMetadataBlocks(inodeData, true)
	if err2 != nil {
		return fmt.Errorf("finalising inode table: %w", err2)
	}

	rootInodeNum := w.nextInode - 1
	rootRawPos := w.inodePos[rootInodeNum]
	rootRef := inodeRef(rootRawPos, inodeCache.offsets)

	if _, err := w.writeMetadataCached(inodeCache); err != nil {
		return fmt.Errorf("writing inode table: %w", err)
	}
	dirTableStart := w.dataPos
	if _, err := w.writeMetadataCached(dirCache); err != nil {
		return fmt.Errorf("writing directory table: %w", err)
	}

	fragTableStart, err := w.writeFragmentTable()
	if err != nil {
		return fmt.Errorf("writing fragment table: %w", err)
	}
	idTableStart, err := w.writeIDTable()
	if err != nil {
		return fmt.Errorf("writing ID table: %w", err)
	}

	bytesUsed := uint64(w.dataPos)

	// Pad to 4KB (not included in bytes_used)
	if pad := (4096 - (w.dataPos % 4096)) % 4096; pad > 0 {
		if _, err := w.out.Write(make([]byte, pad)); err != nil {
			return err
		}
	}

	// Phase 8: Write superblock
	blockLog := uint16(0)
	for bs := w.blockSize; bs > 1; bs >>= 1 {
		blockLog++
	}

	header := SquashfsHeader{
		Magic:               SQUASHFS_MAGIC,
		Inodes:              w.nextInode - 1,
		MkfsTime:            SquashfsMkfsTime(w.mkfsTime.Unix()),
		BlockSize:           w.blockSize,
		Fragments:           uint32(len(w.fragments)),
		Compression:         w.comp,
		BlockLog:            blockLog,
		Flags:               flagNoXattrs | flagDuplicates,
		NoIds:               uint16(len(w.idTable)),
		VersionMajor:        4,
		VersionMinor:        0,
		RootInode:           rootRef,
		BytesUsed:           bytesUsed,
		IdTableStart:        uint64(idTableStart),
		XattrTableStart:     0xFFFFFFFFFFFFFFFF,
		InodeTableStart:     uint64(inodeTableStart),
		DirectoryTableStart: uint64(dirTableStart),
		FragmentTableStart:  uint64(fragTableStart),
		ExportTableStart:    0xFFFFFFFFFFFFFFFF,
	}
	if len(w.fragments) == 0 {
		header.FragmentTableStart = 0xFFFFFFFFFFFFFFFF
	}

	if _, err := w.out.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return binary.Write(w.out, binary.LittleEndian, &header)
}

// processDir recursively processes a directory bottom-up.
func (w *Writer) processDir(dirPath, rootPath string) (uint32, error) {
	names, err := w.listDir(dirPath)
	if err != nil {
		return 0, err
	}
	sort.Strings(names)

	var children []inodeInfo

	for _, name := range names {
		childPath := filepath.Join(dirPath, name)
		info, err := os.Lstat(childPath)
		if err != nil {
			// A path supplied via CreateFromPaths that isn't on disk means
			// the upstream merge (e.g. copyDir over an overlayfs mount)
			// dropped a file that the tar index expected. Skip it here;
			// the caller's integrity check will see the count mismatch
			// and fail the build before caching.
			if os.IsNotExist(err) {
				continue
			}
			return 0, err
		}

		// Apply path filter if set
		if w.pathFilter != nil {
			relPath := "/" + strings.TrimPrefix(strings.TrimPrefix(childPath, rootPath), "/")
			if !info.IsDir() && info.Mode()&fs.ModeSymlink == 0 {
				// Non-directory: skip if filter rejects
				if !w.pathFilter(relPath, false) {
					continue
				}
			} else if info.IsDir() {
				// Directory: skip if filter rejects
				if !w.pathFilter(relPath, true) {
					continue
				}
			} else if info.Mode()&fs.ModeSymlink != 0 {
				if !w.pathFilter(relPath, false) {
					continue
				}
			}
		}

		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(childPath)
			if err != nil {
				return 0, err
			}
			inum := w.buildSymlinkInode(info, target)
			children = append(children, inodeInfo{inum, sqfsTypeSymlink, name})

		case info.IsDir():
			childInum, err := w.processDir(childPath, rootPath)
			if err != nil {
				return 0, err
			}
			children = append(children, inodeInfo{childInum, sqfsTypeDir, name})

		case info.Mode().IsRegular():
			inum, err := w.buildFileInode(childPath, info)
			if err != nil {
				return 0, err
			}
			children = append(children, inodeInfo{inum, sqfsTypeFile, name})
		}
	}

	dirInfo, err := os.Lstat(dirPath)
	if err != nil {
		return 0, err
	}

	dirRawPos, dirSize := w.buildDirEntries(children)
	return w.buildDirInode(dirInfo, children, dirRawPos, dirSize), nil
}

// buildDirEntries writes directory entries to dirRaw with placeholder block references.
// Entries are grouped by inode metadata block (max 256 per group).
// Returns raw byte position and total size.
func (w *Writer) buildDirEntries(children []inodeInfo) (int, uint32) {
	dirRawPos := w.dirRaw.Len()

	if len(children) == 0 {
		return dirRawPos, 0
	}

	sort.Slice(children, func(i, j int) bool {
		return children[i].name < children[j].name
	})

	// Group children by inode metadata block
	var buf bytes.Buffer
	groupStart := 0
	for groupStart < len(children) {
		firstChild := children[groupStart]
		firstBlock := w.inodePos[firstChild.inodeNum] / metadataBlockSize

		// Find extent of this group: same inode metadata block, max 256 entries
		groupEnd := groupStart + 1
		for groupEnd < len(children) && groupEnd-groupStart < 256 {
			childBlock := w.inodePos[children[groupEnd].inodeNum] / metadataBlockSize
			if childBlock != firstBlock {
				break
			}
			groupEnd++
		}

		groupChildren := children[groupStart:groupEnd]

		// Directory header (12 bytes)
		binary.Write(&buf, binary.LittleEndian, uint32(len(groupChildren)-1)) // count - 1

		startFieldOffset := dirRawPos + buf.Len()
		binary.Write(&buf, binary.LittleEndian, uint32(0)) // start (PLACEHOLDER)

		binary.Write(&buf, binary.LittleEndian, firstChild.inodeNum) // inode_number ref

		// Record fixup for this group's "start" field
		w.dirEntryFixups = append(w.dirEntryFixups, dirEntryFixup{
			dirRawOffset:  startFieldOffset,
			firstInodeNum: firstChild.inodeNum,
		})

		// Directory entries
		for _, child := range groupChildren {
			childRawPos := w.inodePos[child.inodeNum]
			offsetInBlock := uint16(childRawPos % metadataBlockSize)
			inodeDelta := int16(int32(child.inodeNum) - int32(firstChild.inodeNum))
			nameBytes := []byte(child.name)

			binary.Write(&buf, binary.LittleEndian, offsetInBlock)
			binary.Write(&buf, binary.LittleEndian, inodeDelta)
			binary.Write(&buf, binary.LittleEndian, child.inodeType)
			binary.Write(&buf, binary.LittleEndian, uint16(len(nameBytes)-1))
			buf.Write(nameBytes)
		}

		groupStart = groupEnd
	}

	dirSize := uint32(buf.Len())
	w.dirRaw.Write(buf.Bytes())

	return dirRawPos, dirSize
}

// unixMode converts an fs.FileMode to the 16-bit on-disk Unix mode
// including setuid/setgid/sticky. Go's FileMode places those bits
// outside the low 12, so .Perm() alone loses them.
func unixMode(m fs.FileMode) uint16 {
	out := uint16(m.Perm())
	if m&fs.ModeSetuid != 0 {
		out |= 0o4000
	}
	if m&fs.ModeSetgid != 0 {
		out |= 0o2000
	}
	if m&fs.ModeSticky != 0 {
		out |= 0o1000
	}
	return out
}

// buildDirInode creates a directory inode with placeholder block refs.
// Uses basic dir inode (type 1) when dirSize fits in uint16, otherwise
// extended dir inode (type 8) with uint32 file_size.
func (w *Writer) buildDirInode(info fs.FileInfo, children []inodeInfo, dirRawPos int, dirSize uint32) uint32 {
	inodeNum := w.allocInode()
	uid := w.lookupID(0)
	gid := w.lookupID(0)

	fileSize := dirSize + 3 // +3 overhead per squashfs spec

	var buf bytes.Buffer

	if fileSize <= 0xFFFF {
		// Basic directory inode (type 1): file_size is uint16
		binary.Write(&buf, binary.LittleEndian, uint16(sqfsTypeDir))
		binary.Write(&buf, binary.LittleEndian, unixMode(info.Mode()))
		binary.Write(&buf, binary.LittleEndian, uid)
		binary.Write(&buf, binary.LittleEndian, gid)
		binary.Write(&buf, binary.LittleEndian, uint32(info.ModTime().Unix()))
		binary.Write(&buf, binary.LittleEndian, inodeNum)

		blockIndexOffset := w.inodeRaw.Len() + 16

		binary.Write(&buf, binary.LittleEndian, uint32(0))               // block_index (PLACEHOLDER)
		binary.Write(&buf, binary.LittleEndian, uint32(len(children)+2)) // link_count
		binary.Write(&buf, binary.LittleEndian, uint16(fileSize))        // file_size
		binary.Write(&buf, binary.LittleEndian, uint16(0))               // block_offset (PLACEHOLDER)
		binary.Write(&buf, binary.LittleEndian, uint32(1))               // parent_inode

		w.appendInode(inodeNum, buf.Bytes())

		if len(children) > 0 {
			w.dirInodeFixups = append(w.dirInodeFixups, dirInodeFixup{
				inodeRawOffset: blockIndexOffset,
				dirRawPos:      dirRawPos,
			})
		}
	} else {
		// Extended directory inode (type 8): file_size is uint32
		binary.Write(&buf, binary.LittleEndian, uint16(sqfsTypeLDir))
		binary.Write(&buf, binary.LittleEndian, unixMode(info.Mode()))
		binary.Write(&buf, binary.LittleEndian, uid)
		binary.Write(&buf, binary.LittleEndian, gid)
		binary.Write(&buf, binary.LittleEndian, uint32(info.ModTime().Unix()))
		binary.Write(&buf, binary.LittleEndian, inodeNum)

		binary.Write(&buf, binary.LittleEndian, uint32(len(children)+2)) // link_count
		binary.Write(&buf, binary.LittleEndian, uint32(fileSize))        // file_size

		blockIndexOffset := w.inodeRaw.Len() + buf.Len()

		binary.Write(&buf, binary.LittleEndian, uint32(0))               // block_index (PLACEHOLDER)
		binary.Write(&buf, binary.LittleEndian, uint32(1))               // parent_inode
		binary.Write(&buf, binary.LittleEndian, uint16(0))               // i_count (no index)
		binary.Write(&buf, binary.LittleEndian, uint16(0))               // block_offset (PLACEHOLDER)
		binary.Write(&buf, binary.LittleEndian, uint32(0))               // xattr_idx (none)

		w.appendInode(inodeNum, buf.Bytes())

		if len(children) > 0 {
			w.dirInodeFixups = append(w.dirInodeFixups, dirInodeFixup{
				inodeRawOffset: blockIndexOffset,
				dirRawPos:      dirRawPos,
			})
		}
	}

	return inodeNum
}

// buildFileInode creates a basic file inode (type 2).
func (w *Writer) buildFileInode(path string, info fs.FileInfo) (uint32, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	startBlock, blockSizes, fragIdx, fragOff, err := w.writeFileDataStreamed(f, info.Size())
	if err != nil {
		return 0, err
	}

	inodeNum := w.allocInode()
	uid := w.lookupID(0)
	gid := w.lookupID(0)

	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(sqfsTypeFile))
	binary.Write(&buf, binary.LittleEndian, unixMode(info.Mode()))
	binary.Write(&buf, binary.LittleEndian, uid)
	binary.Write(&buf, binary.LittleEndian, gid)
	binary.Write(&buf, binary.LittleEndian, uint32(info.ModTime().Unix()))
	binary.Write(&buf, binary.LittleEndian, inodeNum)
	binary.Write(&buf, binary.LittleEndian, startBlock)
	binary.Write(&buf, binary.LittleEndian, fragIdx)
	binary.Write(&buf, binary.LittleEndian, fragOff)
	binary.Write(&buf, binary.LittleEndian, uint32(info.Size()))
	for _, bs := range blockSizes {
		binary.Write(&buf, binary.LittleEndian, bs)
	}

	w.appendInode(inodeNum, buf.Bytes())
	if w.progressFn != nil {
		w.progressFn(w.dataPos)
	}
	return inodeNum, nil
}

// buildSymlinkInode creates a basic symlink inode (type 3).
func (w *Writer) buildSymlinkInode(info fs.FileInfo, target string) uint32 {
	inodeNum := w.allocInode()
	uid := w.lookupID(0)
	gid := w.lookupID(0)

	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(sqfsTypeSymlink))
	binary.Write(&buf, binary.LittleEndian, uint16(0o777))
	binary.Write(&buf, binary.LittleEndian, uid)
	binary.Write(&buf, binary.LittleEndian, gid)
	binary.Write(&buf, binary.LittleEndian, uint32(info.ModTime().Unix()))
	binary.Write(&buf, binary.LittleEndian, inodeNum)
	binary.Write(&buf, binary.LittleEndian, uint32(1))
	binary.Write(&buf, binary.LittleEndian, uint32(len(target)))
	buf.Write([]byte(target))

	w.appendInode(inodeNum, buf.Bytes())
	return inodeNum
}

// writeFragmentTable writes the fragment lookup table.
func (w *Writer) writeFragmentTable() (int64, error) {
	if len(w.fragments) == 0 {
		return w.dataPos, nil
	}

	var fragBuf bytes.Buffer
	for _, f := range w.fragments {
		binary.Write(&fragBuf, binary.LittleEndian, f.start)
		binary.Write(&fragBuf, binary.LittleEndian, f.size)
		binary.Write(&fragBuf, binary.LittleEndian, uint32(0)) // unused
	}

	metaPositions, err := w.writeMetadataBlocksTracked(fragBuf.Bytes())
	if err != nil {
		return 0, err
	}

	tableStart := w.dataPos
	for _, pos := range metaPositions {
		if err := binary.Write(w.out, binary.LittleEndian, uint64(pos)); err != nil {
			return 0, err
		}
		w.dataPos += 8
	}
	return tableStart, nil
}

// writeIDTable writes the UID/GID lookup table.
func (w *Writer) writeIDTable() (int64, error) {
	if len(w.idTable) == 0 {
		w.lookupID(0)
	}

	var idBuf bytes.Buffer
	for _, id := range w.idTable {
		binary.Write(&idBuf, binary.LittleEndian, id)
	}

	metaPositions, err := w.writeMetadataBlocksTracked(idBuf.Bytes())
	if err != nil {
		return 0, err
	}

	tableStart := w.dataPos
	for _, pos := range metaPositions {
		if err := binary.Write(w.out, binary.LittleEndian, uint64(pos)); err != nil {
			return 0, err
		}
		w.dataPos += 8
	}
	return tableStart, nil
}
