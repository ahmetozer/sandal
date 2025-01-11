package detectfs

import (
	"encoding/binary"
	"os"
)

func isExt4(file *os.File) bool {
	// Seek to superblock location
	_, err := file.Seek(1024, 0)
	if err != nil {
		return false
	}

	// Read superblock magic number
	magicBytes := make([]byte, 2)
	_, err = file.Read(magicBytes)
	if err != nil {
		return false
	}

	magic := binary.LittleEndian.Uint16(magicBytes)
	if magic != 0xEF53 {
		return false
	}

	// Read feature flags
	featureBytes := make([]byte, 4)
	_, err = file.Seek(1024+96, 0) // Offset to compatible feature flags
	if err != nil {
		return false
	}
	_, err = file.Read(featureBytes)
	if err != nil {
		return false
	}

	// Check for ext4 specific features
	features := binary.LittleEndian.Uint32(featureBytes)
	ext4Features := uint32(0x68) // Common ext4 features
	return features&ext4Features != 0
}
