package img

import (
	"fmt"
	"os"
)

type Partitions []Partition
type Partition struct {
	Boot   bool
	Size   uint32
	Entry  interface{}
	Scheme PartitionScheme
}

// OpenDiskImage opens the disk image file
func GetImageInfo(path string) (Partitions, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open image: %v", err)
	}
	defer file.Close()

	scheme, err := detectPartitionScheme(file)
	if err != nil {
		return nil, err
	}

	switch scheme {
	case PartitionMBR:
		entries, err := readMBRPartitionTable(file)
		if err != nil {
			return nil, err
		}
		return entries, nil
	case PartitionGPT:
		return nil, fmt.Errorf("GPT is not supported")
	}

	return nil, fmt.Errorf("unkown partition scheme")
}
