package squashfs

import (
	"encoding/binary"
	"fmt"
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
