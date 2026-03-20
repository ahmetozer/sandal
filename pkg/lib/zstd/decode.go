package zstd

import (
	"bytes"
	"io"
)

// Decode decompresses zstd-compressed data and returns the result.
func Decode(data []byte) ([]byte, error) {
	r := NewReader(bytes.NewReader(data))
	return io.ReadAll(r)
}
