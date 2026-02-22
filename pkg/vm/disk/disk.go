package disk

import (
	"fmt"
	"os"
)

func CreateRawDisk(path string, sizeBytes int64) error {
	if sizeBytes <= 0 {
		return fmt.Errorf("disk size must be positive")
	}
	if sizeBytes%512 != 0 {
		return fmt.Errorf("disk size must be a multiple of 512 bytes")
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create disk image: %w", err)
	}
	defer f.Close()
	if err := f.Truncate(sizeBytes); err != nil {
		return fmt.Errorf("truncate disk image: %w", err)
	}
	return nil
}
