package img

import (
	"fmt"
	"os"
)

// OpenDiskImage opens the disk image file
func GetImageInfo(path string) (interface{}, PartitionScheme, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to open image: %v", err)
	}
	defer file.Close()

	scheme, err := detectPartitionScheme(file)
	if err != nil {
		return nil, scheme, err
	}

	switch scheme {
	case PartitionMBR:
		entries, err := readMBRPartitionTable(file)
		if err != nil {
			return nil, scheme, err
		}
		return entries, scheme, nil
	case PartitionGPT:
		header, err := readGPTHeader(file)
		if err != nil {
			return nil, scheme, err
		}
		entries, err := readPartitionEntries(file, header)
		if err != nil {
			return nil, scheme, err
		}
		return entries, scheme, nil
		// return nil, fmt.Errorf("GPT is not supported")
	}

	return nil, scheme, fmt.Errorf("unkown partition scheme")
}
