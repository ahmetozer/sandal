package img

import (
	"encoding/binary"
	"fmt"
	"os"
)

// GPTHeader represents the GPT header structure
type GPTHeader struct {
	Signature         [8]byte
	Revision          [4]byte
	HeaderSize        uint32
	CRC32             uint32
	Reserved          uint32
	CurrentLBA        uint64
	BackupLBA         uint64
	FirstUsableLBA    uint64
	LastUsableLBA     uint64
	DiskGUID          [16]byte
	PartitionEntryLBA uint64
	NumPartEntries    uint32
	PartEntrySize     uint32
	PartArrayCRC32    uint32
}

// GPTPartition represents a GPT partition entry
type GPTPartitionEntry struct {
	PartitionTypeGUID   [16]byte
	UniquePartitionGUID [16]byte
	FirstLBA            uint64
	LastLBA             uint64
	Attributes          uint64
	PartitionName       [72]byte // UTF-16LE string
}

func readGPTHeader(file *os.File) (*GPTHeader, error) {
	// Seek to LBA 1 (where GPT header starts)
	_, err := file.Seek(512, 0) // Skip MBR
	if err != nil {
		return nil, fmt.Errorf("failed to seek to GPT header: %v", err)
	}

	header := &GPTHeader{}
	err = binary.Read(file, binary.LittleEndian, header)
	if err != nil {
		return nil, fmt.Errorf("failed to read GPT header: %v", err)
	}

	// Verify GPT signature
	if string(header.Signature[:]) != "EFI PART" {
		return nil, fmt.Errorf("invalid GPT signature")
	}

	return header, nil
}

// GPT Attribute bits
const (
	RequiredPartition  uint64 = 1 << 0  // Required partition for platform
	NoBlockIOProtocol  uint64 = 1 << 1  // No Block IO Protocol
	LegacyBIOSBootable uint64 = 1 << 2  // Legacy BIOS bootable
	EFIBootable        uint64 = 1 << 63 // EFI bootable partition
)

func readPartitionEntries(file *os.File, header *GPTHeader) ([]GPTPartitionEntry, error) {
	_, err := file.Seek(int64(header.PartitionEntryLBA*512), 0)
	if err != nil {
		return nil, fmt.Errorf("failed to seek to partition entries: %v", err)
	}

	partitions := make([]GPTPartitionEntry, header.NumPartEntries)
	for i := uint32(0); i < header.NumPartEntries; i++ {
		var partition GPTPartitionEntry
		err = binary.Read(file, binary.LittleEndian, &partition)
		if err != nil {
			return nil, fmt.Errorf("failed to read partition entry %d: %v", i, err)
		}

		partitions[i] = partition
	}

	return partitions, nil
}

func (e GPTPartitionEntry) IsLegacyBIOSBootable() bool {
	return e.Attributes&LegacyBIOSBootable != 0
}
func (e GPTPartitionEntry) IsEFIBootable() bool {
	return e.Attributes&EFIBootable != 0
}
func (e GPTPartitionEntry) IsBootable() bool {
	return e.IsEFIBootable() || e.IsLegacyBIOSBootable()
}

func (e GPTPartitionEntry) StartByte() uint64 {
	return e.FirstLBA * 512
}
func (e GPTPartitionEntry) Size() uint64 {
	return (e.LastLBA - e.FirstLBA + 1) * 512
}
func (e GPTPartitionEntry) EndByte() uint64 {
	return e.LastLBA * 512
}

func (header GPTHeader) Print() {
	fmt.Printf("GPT Header:\n")
	fmt.Printf("  Signature: %s\n", string(header.Signature[:]))
	fmt.Printf("  Revision: %x\n", header.Revision)
	fmt.Printf("  Header Size: %d bytes\n", header.HeaderSize)
	fmt.Printf("  First Usable LBA: %d\n", header.FirstUsableLBA)
	fmt.Printf("  Last Usable LBA: %d\n", header.LastUsableLBA)
	fmt.Printf("  Number of Partition Entries: %d\n\n", header.NumPartEntries)
}

func (partition GPTPartitionEntry) Print() {
	// Convert partition name from UTF-16LE to string
	var name string
	for i := 0; i < len(partition.PartitionName); i += 2 {
		if partition.PartitionName[i] == 0 && partition.PartitionName[i+1] == 0 {
			break
		}
		name += string(rune(partition.PartitionName[i]))
	}

	// Only print if partition is not empty (type GUID is not all zeros)
	isEmpty := true
	for _, b := range partition.PartitionTypeGUID {
		if b != 0 {
			isEmpty = false
			break
		}
	}

	if !isEmpty {
		fmt.Printf("Partition:\n")
		fmt.Printf("  Name: %s\n", name)
		fmt.Printf("  First LBA: %d\n", partition.FirstLBA)
		fmt.Printf("  Last LBA: %d\n", partition.LastLBA)
		fmt.Printf("  Size: %.2f GB\n", float64(partition.LastLBA-partition.FirstLBA+1)*512/1024/1024/1024)
		fmt.Printf("  Type GUID: %x-%x-%x-%x-%x\n\n",
			partition.PartitionTypeGUID[0:4],
			partition.PartitionTypeGUID[4:6],
			partition.PartitionTypeGUID[6:8],
			partition.PartitionTypeGUID[8:10],
			partition.PartitionTypeGUID[10:16])
	}
}
