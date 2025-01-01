package img

import (
	"encoding/binary"
	"fmt"
	"os"
)

// PartitionEntry represents a single partition table entry
type MBRPartitionType byte
type MBRPartitionEntry struct {
	Status      byte
	StartHead   byte
	StartSector byte
	StartCyl    byte
	Type        MBRPartitionType
	EndHead     byte
	EndSector   byte
	EndCyl      byte
	StartLBA    uint32
	Sectors     uint32
}

func (d MBRPartitionType) MarshalJSON() ([]byte, error) {
	return []byte("\"" + d.String() + "\""), nil
}

// Master Boot Record and partition table
func readMBRPartitionTable(f *os.File) ([]Partition, error) {
	// Seek to the partition table. The first 446 is executable to loader for bootloader such as grub
	_, err := f.Seek(446, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to seek to partition table: %v", err)
	}

	entries := make([]Partition, 0)

	// Read 4 partition entries because MBR only supports 4 partition
	for i := 0; i < 4; i++ {
		var entry MBRPartitionEntry
		err := binary.Read(f, binary.LittleEndian, &entry)
		if err != nil {
			return nil, fmt.Errorf("failed to read partition entry: %v", err)
		}

		// Only add non-empty partitions
		if entry.Type != 0 {
			part := Partition{Entry: entry,
				Boot:   entry.Status == 0x80,
				Size:   entry.Sectors * 512,
				Scheme: PartitionMBR,
			}
			/*
				Start sector is entry.StartLBA
				End sector is entry.StartLBA+entry.Sectors-1
				Number of sectors entry.Sectors,
			*/
			entries = append(entries, part)
		}

	}

	return entries, nil
}

func (pType MBRPartitionType) String() string {
	types := map[byte]string{
		0x00: "Empty",
		0x01: "FAT12",
		0x04: "FAT16 <32M",
		0x05: "Extended",
		0x06: "FAT16",
		0x07: "NTFS/HPFS",
		0x0b: "FAT32",
		0x0c: "FAT32 (LBA)",
		0x0e: "FAT16 (LBA)",
		0x0f: "Extended (LBA)",
		0x82: "Linux swap",
		0x83: "Linux",
		0x85: "Linux extended",
		0x86: "NTFS volume set",
		0x87: "NTFS volume set",
	}

	if name, ok := types[byte(pType)]; ok {
		return name
	}
	return fmt.Sprintf("Unknown (0x%02x)", string(pType))
}
