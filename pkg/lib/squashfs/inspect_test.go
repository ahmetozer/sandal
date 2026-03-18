package squashfs

import (
	"testing"
)

func TestInspectExisting(t *testing.T) {
	h, err := Info("../alpine-minirootfs-3.21.3.sq")
	if err != nil {
		t.Skip("reference image not available:", err)
	}

	if h.Magic != SQUASHFS_MAGIC {
		t.Errorf("wrong magic: 0x%x", h.Magic)
	}
	if h.Inodes != 521 {
		t.Errorf("expected 521 inodes, got %d", h.Inodes)
	}
	if h.BlockSize != 131072 {
		t.Errorf("expected 128KB block size, got %d", h.BlockSize)
	}
	if h.Compression != 1 {
		t.Errorf("expected gzip compression, got %d", h.Compression)
	}
	if h.VersionMajor != 4 {
		t.Errorf("expected version 4, got %d", h.VersionMajor)
	}
}
