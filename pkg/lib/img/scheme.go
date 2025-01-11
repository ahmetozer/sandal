package img

import (
	"fmt"
	"os"
)

type PartitionScheme uint8

const (
	PartitionMBR PartitionScheme = iota + 1
	PartitionGPT
)

func detectPartitionScheme(file *os.File) (PartitionScheme, error) {
	// Read MBR part
	mbr := make([]byte, 512)
	_, err := file.ReadAt(mbr, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to read MBR: %v", err)
	}

	// Get GPT signature in the second sector
	gpt := make([]byte, 512)
	_, err = file.ReadAt(gpt, 512)
	if err != nil {
		return 0, fmt.Errorf("failed to read GPT header: %v", err)
	}

	// Check GPT signature which is "EFI PART"
	if string(gpt[:8]) == "EFI PART" {
		return PartitionGPT, nil
	}

	// If not GPT, verify it is MBR
	if mbr[510] == 0x55 && mbr[511] == 0xAA {
		return PartitionMBR, nil
	}

	return 0, fmt.Errorf("unknown partition scheme")
}
