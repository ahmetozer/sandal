package mkfs

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"time"
)

const (
	blockSize      = 4096
	inodeSize      = 256
	blocksPerGroup = 32768
	inodesPerGroup = 8192
	firstIno       = 11
	rootIno        = 2

	inodeTableBlocks = inodesPerGroup * inodeSize / blockSize // 512

	// Feature flags
	featureCompatExtAttr       = 0x0008
	featureIncompatFiletype    = 0x0002
	featureIncompatExtents     = 0x0040
	featureRoCompatSparseSuper = 0x0001
	featureRoCompatExtraIsize  = 0x0040
)

// hasSuperblockBackup returns true if the group has a backup superblock + GDT.
// With SPARSE_SUPER: groups 0, 1, and powers of 3, 5, 7.
func hasSuperblockBackup(g uint32) bool {
	if g <= 1 {
		return true
	}
	for _, p := range []uint32{3, 5, 7} {
		n := p
		for n < g {
			n *= p
		}
		if n == g {
			return true
		}
	}
	return false
}

// FormatExt4 writes an empty ext4 filesystem to the file at path.
// The file must already exist at the desired size (e.g., created by
// disk.CreateRawDisk). The filesystem uses the full file size,
// creating as many block groups as needed.
func FormatExt4(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	sizeBytes, err := f.Seek(0, 2)
	if err != nil {
		return fmt.Errorf("seek end: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("seek start: %w", err)
	}

	totalBlocks := uint32(sizeBytes / blockSize)
	numGroups := (totalBlocks + blocksPerGroup - 1) / blocksPerGroup
	if numGroups == 0 {
		numGroups = 1
	}
	gdtBlocks := (numGroups*32 + blockSize - 1) / blockSize
	// Blocks reserved for SB + GDT in groups that have backups
	sbGdtBlocks := 1 + gdtBlocks

	totalInodes := numGroups * inodesPerGroup
	now := uint32(time.Now().Unix())

	type groupInfo struct {
		start       uint32
		bbm         uint32
		ibm         uint32
		itable      uint32
		firstData   uint32
		overhead    uint32 // metadata blocks consumed in this group
		blocksInGrp uint32
		hasBackup   bool
	}

	groups := make([]groupInfo, numGroups)
	var totalFreeBlocks uint32
	var totalFreeInodes uint32

	for g := uint32(0); g < numGroups; g++ {
		gi := &groups[g]
		gi.start = g * blocksPerGroup
		gi.blocksInGrp = blocksPerGroup
		if gi.start+gi.blocksInGrp > totalBlocks {
			gi.blocksInGrp = totalBlocks - gi.start
		}
		gi.hasBackup = hasSuperblockBackup(g)

		if gi.hasBackup {
			// SB + GDT + block bitmap + inode bitmap + inode table
			gi.bbm = gi.start + sbGdtBlocks
			gi.ibm = gi.bbm + 1
			gi.itable = gi.ibm + 1
			gi.firstData = gi.itable + inodeTableBlocks
			gi.overhead = sbGdtBlocks + 2 + inodeTableBlocks // sb+gdt + 2 bitmaps + itable
		} else {
			// block bitmap + inode bitmap + inode table
			gi.bbm = gi.start
			gi.ibm = gi.bbm + 1
			gi.itable = gi.ibm + 1
			gi.firstData = gi.itable + inodeTableBlocks
			gi.overhead = 2 + inodeTableBlocks // 2 bitmaps + itable
		}

		if gi.overhead >= gi.blocksInGrp {
			// Group too small for metadata — mark as fully used
			continue
		}

		freeBlocks := gi.blocksInGrp - gi.overhead
		if g == 0 {
			freeBlocks-- // root dir data block
		}
		totalFreeBlocks += freeBlocks

		freeInodes := uint32(inodesPerGroup)
		if g == 0 {
			freeInodes -= firstIno
		}
		totalFreeInodes += freeInodes
	}

	// 1. Superblock at file offset 1024
	sb := make([]byte, 1024)
	put32 := func(off int, v uint32) { binary.LittleEndian.PutUint32(sb[off:], v) }
	put16 := func(off int, v uint16) { binary.LittleEndian.PutUint16(sb[off:], v) }

	put32(0x00, totalInodes)
	put32(0x04, totalBlocks)
	put32(0x08, totalBlocks*5/100)
	put32(0x0C, totalFreeBlocks)
	put32(0x10, totalFreeInodes)
	put32(0x14, 0)              // s_first_data_block
	put32(0x18, 2)              // s_log_block_size (4KB)
	put32(0x1C, 2)              // s_log_cluster_size
	put32(0x20, blocksPerGroup) // s_blocks_per_group
	put32(0x24, blocksPerGroup) // s_clusters_per_group
	put32(0x28, inodesPerGroup) // s_inodes_per_group
	put32(0x30, now)            // s_wtime
	put16(0x36, 0xFFFF)         // s_max_mnt_count
	put16(0x38, 0xEF53)         // s_magic
	put16(0x3A, 1)              // s_state (clean)
	put16(0x3C, 1)              // s_errors (continue)
	put32(0x4C, 1)              // s_rev_level (dynamic)
	put32(0x54, firstIno)       // s_first_ino
	put16(0x58, inodeSize)      // s_inode_size
	put32(0x5C, featureCompatExtAttr)
	put32(0x60, featureIncompatFiletype|featureIncompatExtents)
	put32(0x64, featureRoCompatSparseSuper|featureRoCompatExtraIsize)
	rand.Read(sb[0x68 : 0x68+16]) // s_uuid
	put16(0xFE, 32)               // s_desc_size
	put32(0x108, now)              // s_mkfs_time
	put16(0x15C, 32)               // s_min_extra_isize
	put16(0x15E, 32)               // s_want_extra_isize

	if _, err := f.WriteAt(sb, 1024); err != nil {
		return fmt.Errorf("write superblock: %w", err)
	}

	// 2. Group descriptor table
	gdtBuf := make([]byte, gdtBlocks*blockSize)
	for g := uint32(0); g < numGroups; g++ {
		gi := &groups[g]
		off := g * 32

		freeBlocks := uint32(0)
		freeInodes := uint32(inodesPerGroup)
		usedDirs := uint16(0)
		itableUnused := uint16(inodesPerGroup)

		if gi.overhead < gi.blocksInGrp {
			freeBlocks = gi.blocksInGrp - gi.overhead
		}
		if g == 0 {
			freeBlocks--
			freeInodes -= firstIno
			usedDirs = 1
			itableUnused = inodesPerGroup - firstIno
		}

		binary.LittleEndian.PutUint32(gdtBuf[off+0:], gi.bbm)
		binary.LittleEndian.PutUint32(gdtBuf[off+4:], gi.ibm)
		binary.LittleEndian.PutUint32(gdtBuf[off+8:], gi.itable)
		binary.LittleEndian.PutUint16(gdtBuf[off+12:], uint16(freeBlocks))
		binary.LittleEndian.PutUint16(gdtBuf[off+14:], uint16(freeInodes))
		binary.LittleEndian.PutUint16(gdtBuf[off+16:], usedDirs)
		binary.LittleEndian.PutUint16(gdtBuf[off+28:], itableUnused) // bg_itable_unused
	}

	// Write primary GDT in group 0
	if _, err := f.WriteAt(gdtBuf, blockSize); err != nil {
		return fmt.Errorf("write group descriptors: %w", err)
	}

	// Write backup SB + GDT in groups that have backups (except group 0)
	for g := uint32(1); g < numGroups; g++ {
		if !groups[g].hasBackup {
			continue
		}
		gi := &groups[g]
		backupOffset := int64(gi.start) * blockSize
		// Backup superblock (write at offset 0 of the group's first block)
		if _, err := f.WriteAt(sb, backupOffset); err != nil {
			return fmt.Errorf("write backup superblock group %d: %w", g, err)
		}
		// Backup GDT right after
		if _, err := f.WriteAt(gdtBuf, backupOffset+blockSize); err != nil {
			return fmt.Errorf("write backup GDT group %d: %w", g, err)
		}
	}

	// 3. Write bitmaps for each group
	for g := uint32(0); g < numGroups; g++ {
		gi := &groups[g]

		// Block bitmap — bits are relative to group start
		bbm := make([]byte, blockSize)
		// Mark overhead blocks as used
		overheadBits := gi.overhead
		if g == 0 {
			overheadBits++ // +1 for root dir data block
		}
		for i := uint32(0); i < overheadBits; i++ {
			bbm[i/8] |= 1 << (i % 8)
		}
		// Mark bits beyond actual blocks in last group
		if gi.blocksInGrp < blocksPerGroup {
			for i := gi.blocksInGrp; i < blocksPerGroup; i++ {
				bbm[i/8] |= 1 << (i % 8)
			}
		}
		if _, err := f.WriteAt(bbm, int64(gi.bbm)*blockSize); err != nil {
			return fmt.Errorf("write block bitmap group %d: %w", g, err)
		}

		// Inode bitmap
		ibm := make([]byte, blockSize)
		if g == 0 {
			ibm[0] = 0xFF // inodes 1-8
			ibm[1] = 0x03 // inodes 9-10
		}
		if _, err := f.WriteAt(ibm, int64(gi.ibm)*blockSize); err != nil {
			return fmt.Errorf("write inode bitmap group %d: %w", g, err)
		}
	}

	// 4. Root inode (inode 2)
	g0 := &groups[0]
	inode := make([]byte, inodeSize)
	iput16 := func(off int, v uint16) { binary.LittleEndian.PutUint16(inode[off:], v) }
	iput32 := func(off int, v uint32) { binary.LittleEndian.PutUint32(inode[off:], v) }

	iput16(0x00, 0x41ED)    // i_mode (S_IFDIR | 0755)
	iput32(0x04, blockSize) // i_size_lo
	iput32(0x08, now)       // i_atime
	iput32(0x0C, now)       // i_ctime
	iput32(0x10, now)       // i_mtime
	iput16(0x1A, 2)         // i_links_count
	iput32(0x1C, 8)         // i_blocks_lo (sectors)
	iput32(0x20, 0x80000)   // i_flags (EXTENTS)

	// Extent tree header
	iput16(0x28, 0xF30A)
	iput16(0x2A, 1)
	iput16(0x2C, 4)
	iput16(0x2E, 0)
	iput32(0x30, 0)
	// Extent entry
	iput32(0x34, 0)
	iput16(0x38, 1)
	iput16(0x3A, 0)
	iput32(0x3C, g0.firstData)
	iput16(0x80, 32) // i_extra_isize

	inodeOffset := int64(g0.itable)*blockSize + int64(1)*inodeSize
	if _, err := f.WriteAt(inode, inodeOffset); err != nil {
		return fmt.Errorf("write root inode: %w", err)
	}

	// 5. Root directory data block
	dir := make([]byte, blockSize)
	dput16 := func(off int, v uint16) { binary.LittleEndian.PutUint16(dir[off:], v) }
	dput32 := func(off int, v uint32) { binary.LittleEndian.PutUint32(dir[off:], v) }

	dput32(0, rootIno)
	dput16(4, 12)
	dir[6] = 1
	dir[7] = 2
	dir[8] = '.'

	dput32(12, rootIno)
	dput16(16, uint16(blockSize-12))
	dir[18] = 2
	dir[19] = 2
	dir[20] = '.'
	dir[21] = '.'

	if _, err := f.WriteAt(dir, int64(g0.firstData)*blockSize); err != nil {
		return fmt.Errorf("write root directory: %w", err)
	}

	return nil
}
