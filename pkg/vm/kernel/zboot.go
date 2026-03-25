package kernel

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
)

// decompressZBoot checks if data is an EFI ZBOOT compressed kernel (magic "zimg"
// at offset 4) and decompresses the gzip payload to produce the raw ARM64 Image.
// If the kernel is not ZBOOT, the data is returned unchanged.
func decompressZBoot(data []byte) ([]byte, error) {
	if len(data) < 16 {
		return data, nil
	}
	if string(data[4:8]) != "zimg" {
		return data, nil
	}

	payloadOffset := binary.LittleEndian.Uint32(data[8:12])
	payloadSize := binary.LittleEndian.Uint32(data[12:16])

	if uint64(payloadOffset)+uint64(payloadSize) > uint64(len(data)) {
		return nil, fmt.Errorf("ZBOOT payload extends beyond file (offset=%d size=%d filesize=%d)", payloadOffset, payloadSize, len(data))
	}

	slog.Info("decompressing EFI ZBOOT kernel")

	gr, err := gzip.NewReader(bytes.NewReader(data[payloadOffset : payloadOffset+payloadSize]))
	if err != nil {
		return nil, fmt.Errorf("ZBOOT gzip: %w", err)
	}
	defer gr.Close()

	raw, err := io.ReadAll(gr)
	if err != nil {
		return nil, fmt.Errorf("ZBOOT decompress: %w", err)
	}
	return raw, nil
}
