package squashfs

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"
)

const SquashfsHeaderSize = 96

func Info(path string) (SquashfsHeader, error) {
	var header SquashfsHeader

	file, err := os.Open(path)
	if err != nil {
		return header, fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	err = binary.Read(file, binary.LittleEndian, &header)
	if err != nil {
		return header, fmt.Errorf("failed to read squashfs header: %v", err)
	}

	if header.Magic != SQUASHFS_MAGIC && header.Magic != SQUASHFS_MAGIC_LE {
		return header, fmt.Errorf("not supported squashfs file")
	}

	return header, nil
}

func (header SquashfsHeader) Print() error {
	fmt.Printf("Endianness: %s\n", header.Magic)
	fmt.Printf("Compression: %s\n", header.Compression)
	fmt.Printf("Block Size: %d bytes\n", header.BlockSize)
	fmt.Printf("Total Inodes: %d\n", header.Inodes)
	fmt.Printf("Total Size: %.2f MB\n", float64(header.BytesUsed)/(1024*1024))
	fmt.Printf("Created: %v\n", time.Unix(int64(header.MkfsTime), 0))
	return nil
}

// CountRegularFiles walks a squashfs file's inode table and returns the
// number of type-2 (regular file) inodes. Used to validate a freshly
// written image against the source directory before caching it.
//
// Only supports inode types emitted by our own writer (basic dir,
// basic file, basic symlink, extended dir). On an unknown type the
// walker stops early and returns the count so far plus an error.
func CountRegularFiles(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var hdr SquashfsHeader
	if err := binary.Read(f, binary.LittleEndian, &hdr); err != nil {
		return 0, fmt.Errorf("read superblock: %w", err)
	}
	if hdr.Magic != SQUASHFS_MAGIC && hdr.Magic != SQUASHFS_MAGIC_LE {
		return 0, fmt.Errorf("not a squashfs image: bad magic 0x%x", uint32(hdr.Magic))
	}

	if _, err := f.Seek(int64(hdr.InodeTableStart), io.SeekStart); err != nil {
		return 0, err
	}
	inodeTableEnd := int64(hdr.DirectoryTableStart)

	// Concatenate uncompressed metadata blocks from the inode table.
	var raw bytes.Buffer
	for {
		pos, _ := f.Seek(0, io.SeekCurrent)
		if pos >= inodeTableEnd {
			break
		}
		var blockHdr uint16
		if err := binary.Read(f, binary.LittleEndian, &blockHdr); err != nil {
			return 0, fmt.Errorf("read block hdr at %d: %w", pos, err)
		}
		size := int(blockHdr & 0x7FFF)
		uncomp := blockHdr&0x8000 != 0
		chunk := make([]byte, size)
		if _, err := io.ReadFull(f, chunk); err != nil {
			return 0, fmt.Errorf("read block at %d: %w", pos, err)
		}
		if uncomp {
			raw.Write(chunk)
		} else {
			zr, err := zlib.NewReader(bytes.NewReader(chunk))
			if err != nil {
				return 0, fmt.Errorf("zlib reader at %d: %w", pos, err)
			}
			dec, err := io.ReadAll(zr)
			zr.Close()
			if err != nil {
				return 0, fmt.Errorf("decompress block at %d: %w", pos, err)
			}
			raw.Write(dec)
		}
	}

	blockSize := int(hdr.BlockSize)
	if blockSize == 0 {
		blockSize = 131072
	}
	buf := raw.Bytes()
	count := 0
	i := 0
	for seen := uint32(0); seen < hdr.Inodes && i+2 <= len(buf); seen++ {
		itype := binary.LittleEndian.Uint16(buf[i:])
		sz, ok := inodeSize(itype, buf[i:], blockSize)
		if !ok {
			return count, fmt.Errorf("unknown inode type %d at offset %d", itype, i)
		}
		if itype == 2 {
			count++
		}
		i += sz
	}
	return count, nil
}

// inodeSize returns the on-disk size of the inode starting at buf[0:]
// for the inode types our writer emits (1, 2, 3, 8).
func inodeSize(itype uint16, buf []byte, blockSize int) (int, bool) {
	switch itype {
	case 1: // basic dir
		return 32, true
	case 2: // basic file: 32-byte fixed header + 4 bytes per full data block
		if len(buf) < 32 {
			return 0, false
		}
		fileSize := binary.LittleEndian.Uint32(buf[28:])
		nBlocks := int(fileSize) / blockSize
		return 32 + 4*nBlocks, true
	case 3: // basic symlink: 24 bytes + target
		if len(buf) < 24 {
			return 0, false
		}
		targetLen := binary.LittleEndian.Uint32(buf[20:])
		return 24 + int(targetLen), true
	case 8: // extended dir
		return 40, true
	}
	return 0, false
}
