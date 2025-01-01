package img

import (
	"fmt"
	"os"
)

type Partition struct {
	Boot   bool
	Size   uint32
	Entry  interface{}
	Scheme PartitionScheme
}

// OpenDiskImage opens the disk image file
func GetImageInfo(path string) ([]Partition, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open image: %v", err)
	}
	defer file.Close()

	scheme, err := detectPartitionScheme(file)
	if err != nil {
		return nil, err
	}

	if scheme == PartitionMBR {
		entries, err := readMBRPartitionTable(file)
		if err != nil {
			return nil, err
		}
		return entries, nil
	}
	return nil, fmt.Errorf("only MBR partition scheme is supported")
}
