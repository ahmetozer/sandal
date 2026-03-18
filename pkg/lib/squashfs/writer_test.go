package squashfs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ahmetozer/sandal/pkg/lib/alpine"
)

// TestCreateLinuxRootFs downloads the latest Alpine minirootfs, extracts it,
// and creates a squashfs image using the Go squashfs writer.
// The output is written to .testing/squashfs/alpine.sqfs relative to the repo root.
func TestCreateLinuxRootFs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network-dependent test in short mode")
	}

	// Discover the latest Alpine minirootfs
	version, tarballURL, err := alpine.DiscoverLatestMinirootfs()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Using Alpine %s: %s", version, tarballURL)

	// Download and extract to temp directory
	rootfsDir := t.TempDir()
	t.Logf("Downloading and extracting to %s", rootfsDir)
	if err := alpine.DownloadRootfs(tarballURL, rootfsDir); err != nil {
		t.Fatal(err)
	}

	// Determine output path: repo_root/.testing/squashfs/alpine.sqfs
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(repoRoot, ".testing", "squashfs")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(outDir, "alpine.sqfs")

	// Create squashfs image
	t.Logf("Creating squashfs image at %s", outPath)
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

	if err := w.CreateFromDir(rootfsDir); err != nil {
		t.Fatal("CreateFromDir failed:", err)
	}
	f.Close()

	// Verify
	h, err := Info(outPath)
	if err != nil {
		t.Fatal("reading back header:", err)
	}

	if h.Magic != SQUASHFS_MAGIC {
		t.Errorf("wrong magic: 0x%x", h.Magic)
	}
	if h.Inodes == 0 {
		t.Error("no inodes")
	}
	if h.VersionMajor != 4 {
		t.Errorf("wrong version: %d", h.VersionMajor)
	}

	fi, _ := os.Stat(outPath)
	t.Logf("Created squashfs: %d inodes, %.2f MB", h.Inodes, float64(fi.Size())/(1024*1024))
}
