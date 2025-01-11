package squashfs

import (
	"fmt"
	"time"
)

type SquashfsMagic uint32
type SquashfsMkfsTime uint32

type SquashfsCompression uint16
type SquashfsVersion uint16

// SquashfsHeader represents the squashfs superblock structure
type SquashfsHeader struct {
	Magic               SquashfsMagic
	Inodes              uint32
	MkfsTime            SquashfsMkfsTime
	BlockSize           uint32
	Fragments           uint32
	Compression         SquashfsCompression
	BlockLog            uint16
	Flags               uint16
	NoIds               uint16
	Version             SquashfsVersion
	RootInode           uint64
	BytesUsed           uint64
	IdTableStart        uint64
	XattrTableStart     uint64
	InodeTableStart     uint64
	DirectoryTableStart uint64
	FragmentTableStart  uint64
	ExportTableStart    uint64
}

func (m SquashfsMagic) String() string {
	return map[bool]string{true: "Little", false: "Big"}[m == SQUASHFS_MAGIC_LE]
}
func (d SquashfsMagic) MarshalJSON() ([]byte, error) {
	return []byte("\"" + d.String() + "\""), nil
}

func (t SquashfsMkfsTime) String() string {
	return time.Unix(int64(t), 0).String()
}
func (d SquashfsMkfsTime) MarshalJSON() ([]byte, error) {
	return []byte("\"" + d.String() + "\""), nil
}

func (v SquashfsVersion) String() string {
	return fmt.Sprintf("%d.%d", v&0xFF, v>>8)
}
func (d SquashfsVersion) MarshalJSON() ([]byte, error) {
	return []byte("\"" + d.String() + "\""), nil
}

func (v SquashfsCompression) String() string {
	t, ok := compressionTypes[v]
	if ok {
		return t
	}
	return fmt.Sprintf("%d", v)
}
func (d SquashfsCompression) MarshalJSON() ([]byte, error) {
	return []byte("\"" + d.String() + "\""), nil
}

// Known squashfs magic numbers
const (
	SQUASHFS_MAGIC    = 0x73717368
	SQUASHFS_MAGIC_LE = 0x68737173
)

// Compression types
var compressionTypes = map[SquashfsCompression]string{
	1: "gzip",
	2: "lzma",
	3: "lzo",
	4: "xz",
	5: "lz4",
	6: "zstd",
}
