//go:build linux

package squash

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// unixMode returns the 12 bits of a file's mode as the kernel reports them
// (including setuid/setgid/sticky). Bypasses os.FileMode's alternate
// encoding so the test failure message reflects real on-disk mode.
func unixMode(t *testing.T, path string) uint32 {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	return st.Mode & 0o7777
}

// writeTar writes a tarball from the given entries into a bytes.Buffer.
// Each entry must have Name, Typeflag, and Mode set.
func writeTar(t *testing.T, entries []tar.Header, bodies map[string][]byte) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range entries {
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatalf("tar header %s: %v", h.Name, err)
		}
		if body, ok := bodies[h.Name]; ok {
			if _, err := tw.Write(body); err != nil {
				t.Fatalf("tar body %s: %v", h.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return &buf
}

// TestExtractLayerRawPreservesSpecialModeBits covers the tar-read path.
// extractLayerRaw must retain setuid, setgid, sticky, and group-write bits
// on files and directories as they appear in the tar header.
func TestExtractLayerRawPreservesSpecialModeBits(t *testing.T) {
	dir := t.TempDir()

	// Ensure a default-ish umask so the test reflects production behavior.
	// 022 would mask group-write; we leave it in place to prove the code
	// re-applies the mode after creation rather than relying on umask==0.
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })

	suidBody := []byte("#!/bin/true\n")
	sgidBody := []byte("#!/bin/true\n")
	grpBody := []byte("group writable")

	entries := []tar.Header{
		{Name: "stickydir/", Typeflag: tar.TypeDir, Mode: 0o1777},
		{Name: "suid-bin", Typeflag: tar.TypeReg, Mode: 0o4755, Size: int64(len(suidBody))},
		{Name: "sgid-bin", Typeflag: tar.TypeReg, Mode: 0o2755, Size: int64(len(sgidBody))},
		{Name: "grpwrite.sh", Typeflag: tar.TypeReg, Mode: 0o664, Size: int64(len(grpBody))},
	}
	buf := writeTar(t, entries, map[string][]byte{
		"suid-bin":    suidBody,
		"sgid-bin":    sgidBody,
		"grpwrite.sh": grpBody,
	})

	if err := extractLayerRaw(buf, dir, nil); err != nil {
		t.Fatalf("extractLayerRaw: %v", err)
	}

	cases := []struct {
		rel  string
		want uint32
	}{
		{"stickydir", 0o1777},
		{"suid-bin", 0o4755},
		{"sgid-bin", 0o2755},
		{"grpwrite.sh", 0o664},
	}
	for _, c := range cases {
		got := unixMode(t, filepath.Join(dir, c.rel))
		if got != c.want {
			t.Errorf("%s: got mode 0o%o, want 0o%o", c.rel, got, c.want)
		}
	}
}

// TestApplyLayerDirPreservesSpecialModeBits covers the disk-copy merge path.
// applyLayerDir copies a pre-extracted layer into outDir; it must not lose
// mode bits that extractLayerRaw already placed on disk.
func TestApplyLayerDirPreservesSpecialModeBits(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })

	// Prepare a source layer dir with special-bit modes pre-set.
	mustWrite := func(rel string, body []byte, mode os.FileMode) {
		full := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, body, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(full, mode); err != nil {
			t.Fatal(err)
		}
	}
	mustMkdir := func(rel string, mode os.FileMode) {
		full := filepath.Join(src, rel)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(full, mode); err != nil {
			t.Fatal(err)
		}
	}
	mustMkdir("stickydir", os.ModeSticky|0o777)
	mustWrite("suid-bin", []byte("x"), os.ModeSetuid|0o755)
	mustWrite("sgid-bin", []byte("x"), os.ModeSetgid|0o755)
	mustWrite("grpwrite.sh", []byte("y"), 0o664)

	if err := applyLayerDir(src, dst); err != nil {
		t.Fatalf("applyLayerDir: %v", err)
	}

	cases := []struct {
		rel  string
		want uint32
	}{
		{"stickydir", 0o1777},
		{"suid-bin", 0o4755},
		{"sgid-bin", 0o2755},
		{"grpwrite.sh", 0o664},
	}
	for _, c := range cases {
		got := unixMode(t, filepath.Join(dst, c.rel))
		if got != c.want {
			t.Errorf("%s: got mode 0o%o, want 0o%o", c.rel, got, c.want)
		}
	}
}

// TestCopyDirPreservesSpecialModeBits covers the overlayfs-merge copyDir path
// (mergeWithOverlayfs -> copyDir). This runs when an overlayfs mount is
// available and copies the merged view to outDir.
func TestCopyDirPreservesSpecialModeBits(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })

	mustWrite := func(rel string, body []byte, mode os.FileMode) {
		full := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, body, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(full, mode); err != nil {
			t.Fatal(err)
		}
	}
	mustMkdir := func(rel string, mode os.FileMode) {
		full := filepath.Join(src, rel)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(full, mode); err != nil {
			t.Fatal(err)
		}
	}
	mustMkdir("stickydir", os.ModeSticky|0o777)
	mustWrite("suid-bin", []byte("x"), os.ModeSetuid|0o755)
	mustWrite("grpwrite.sh", []byte("y"), 0o664)

	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copyDir: %v", err)
	}

	cases := []struct {
		rel  string
		want uint32
	}{
		{"stickydir", 0o1777},
		{"suid-bin", 0o4755},
		{"grpwrite.sh", 0o664},
	}
	for _, c := range cases {
		got := unixMode(t, filepath.Join(dst, c.rel))
		if got != c.want {
			t.Errorf("%s: got mode 0o%o, want 0o%o", c.rel, got, c.want)
		}
	}
}
