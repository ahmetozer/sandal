package squashfs

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func TestCreateFromAlpineRoot(t *testing.T) {
	srcDir := "../alpine/root"
	if _, err := os.Stat(srcDir); err != nil {
		t.Skip("alpine root not available:", err)
	}

	outPath := "/tmp/test-squashfs-output.sq"
	defer os.Remove(outPath)

	f, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	w, err := NewWriter(f,
		WithBlockSize(131072),
		WithCompression(CompGzip),
		WithMkfsTime(time.Now()),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := w.CreateFromDir(srcDir); err != nil {
		t.Fatal("CreateFromDir failed:", err)
	}
	f.Close()

	// Verify by reading back the header
	h, err := Info(outPath)
	if err != nil {
		t.Fatal("reading back header:", err)
	}

	fmt.Println("=== Created squashfs image ===")
	h.Print()
	fmt.Printf("Root Inode Ref: 0x%x\n", h.RootInode)
	fmt.Printf("Inodes: %d\n", h.Inodes)
	fmt.Printf("Fragments: %d\n", h.Fragments)
	fmt.Printf("IDs: %d\n", h.NoIds)
	fmt.Printf("Flags: 0x%04x\n", h.Flags)
	fmt.Printf("InodeTableStart: 0x%x\n", h.InodeTableStart)
	fmt.Printf("DirTableStart: 0x%x\n", h.DirectoryTableStart)
	fmt.Printf("FragTableStart: 0x%x\n", h.FragmentTableStart)
	fmt.Printf("IdTableStart: 0x%x\n", h.IdTableStart)

	// Basic sanity checks
	if h.Magic != SQUASHFS_MAGIC {
		t.Errorf("wrong magic: 0x%x", h.Magic)
	}
	if h.Inodes == 0 {
		t.Error("no inodes")
	}
	if h.BlockSize != 131072 {
		t.Errorf("wrong block size: %d", h.BlockSize)
	}
	if h.Compression != CompGzip {
		t.Errorf("wrong compression: %d", h.Compression)
	}
	if h.VersionMajor != 4 {
		t.Errorf("wrong version: %d", h.VersionMajor)
	}

	// Print file size
	fi, _ := os.Stat(outPath)
	fmt.Printf("Output file size: %.2f MB\n", float64(fi.Size())/(1024*1024))
}
