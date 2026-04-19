package squash

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmetozer/sandal/pkg/lib/squashfs"
)

func TestCountRegularFilesMatchesDirectory(t *testing.T) {
	dir := t.TempDir()

	files := []string{
		"a.txt",
		"sub/b.txt",
		"sub/deep/c.txt",
	}
	for _, rel := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("hello"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("a.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	got, err := countRegularFiles(dir)
	if err != nil {
		t.Fatalf("countRegularFiles: %v", err)
	}
	if got != len(files) {
		t.Fatalf("count mismatch: got %d want %d", got, len(files))
	}
}

func TestCountRegularFilesRoundTripsSquashfs(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{"x.txt", "y/z.txt"} {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sqfsPath := filepath.Join(t.TempDir(), "out.sqfs")
	out, err := os.Create(sqfsPath)
	if err != nil {
		t.Fatal(err)
	}
	w, err := squashfs.NewWriter(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.CreateFromDir(dir); err != nil {
		t.Fatal(err)
	}
	out.Close()

	srcCount, err := countRegularFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	sqfsCount, err := squashfs.CountRegularFiles(sqfsPath)
	if err != nil {
		t.Fatalf("squashfs.CountRegularFiles: %v", err)
	}
	if srcCount != sqfsCount {
		t.Fatalf("sqfs=%d src=%d", sqfsCount, srcCount)
	}
	if srcCount != 2 {
		t.Fatalf("expected 2, got src=%d", srcCount)
	}
}
