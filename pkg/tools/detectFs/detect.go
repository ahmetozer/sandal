package detectfs

import (
	"bytes"
	"fmt"
	"os"
)

func DetectFilesystem(devicePath string) (string, error) {
	file, err := os.OpenFile(devicePath, os.O_RDONLY, 0)
	if err != nil {
		return "", fmt.Errorf("failed to open device: %v", err)
	}
	defer file.Close()

	// Buffer for reading filesystem signatures
	buf := make([]byte, 4096)

	for _, sig := range fsSignatures {
		_, err := file.Seek(int64(sig.offset), 0)
		if err != nil {
			continue
		}

		n, err := file.Read(buf)
		if err != nil || n < len(sig.magic) {
			continue
		}

		if bytes.Equal(buf[:len(sig.magic)], sig.magic) {
			return sig.fstype, nil
		}
	}

	// Additional check for ext4 specific features
	if isExt4(file) {
		return "ext4", nil
	}

	return "", fmt.Errorf("unknown filesystem type")
}
