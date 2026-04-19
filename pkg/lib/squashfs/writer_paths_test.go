//go:build linux

package squashfs

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// unsquashfsPaths returns the list of relative paths that the on-disk
// squashfs image contains, excluding the synthetic "squashfs-root" prefix
// that unsquashfs prepends. Used to verify which entries the writer stored.
func unsquashfsPaths(t *testing.T, sqfs string) []string {
	t.Helper()
	out, err := exec.Command("unsquashfs", "-lln", sqfs).Output()
	if err != nil {
		t.Fatalf("unsquashfs: %v", err)
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		p := fields[len(fields)-1]
		if p == "squashfs-root" {
			continue
		}
		p = strings.TrimPrefix(p, "squashfs-root/")
		names = append(names, p)
	}
	sort.Strings(names)
	return names
}

// TestCreateFromPathsHonorsInputList proves the writer uses the caller-
// supplied path list rather than enumerating the directory. We place 5
// files on disk but only hand 3 to CreateFromPaths; the resulting sqfs
// must contain exactly those 3 (plus their ancestor directories).
func TestCreateFromPathsHonorsInputList(t *testing.T) {
	if _, err := exec.LookPath("unsquashfs"); err != nil {
		t.Skip("unsquashfs not installed")
	}
	src := t.TempDir()
	for _, rel := range []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt", "sub/x.txt", "sub/y.txt"} {
		full := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sqfs := filepath.Join(t.TempDir(), "subset.sqfs")
	out, err := os.Create(sqfs)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWriter(out, WithCompression(CompGzip))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.CreateFromPaths(src, []string{"a.txt", "c.txt", "sub/x.txt"}); err != nil {
		t.Fatal(err)
	}
	out.Close()

	got := unsquashfsPaths(t, sqfs)
	want := []string{"a.txt", "c.txt", "sub", "sub/x.txt"}
	if !equalStrings(got, want) {
		t.Errorf("paths in sqfs:\n got %v\nwant %v", got, want)
	}
}

// TestCreateFromPathsFullListMatchesCreateFromDir confirms that when the
// path list matches everything on disk, CreateFromPaths and CreateFromDir
// produce the same entry set. Guards against hierarchy bugs in the
// path->tree reconstruction.
func TestCreateFromPathsFullListMatchesCreateFromDir(t *testing.T) {
	if _, err := exec.LookPath("unsquashfs"); err != nil {
		t.Skip("unsquashfs not installed")
	}
	src := t.TempDir()
	all := []string{"top.txt", "dir1/a.txt", "dir1/b.txt", "dir1/nested/deep.txt", "dir2/c.txt"}
	for _, rel := range all {
		full := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Also include a symlink.
	if err := os.Symlink("top.txt", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}

	sqfsA := buildWithCreateFromDir(t, src)
	sqfsB := buildWithCreateFromPaths(t, src, append(all, "link"))

	aPaths := unsquashfsPaths(t, sqfsA)
	bPaths := unsquashfsPaths(t, sqfsB)
	if !equalStrings(aPaths, bPaths) {
		t.Errorf("entry sets differ:\n CreateFromDir=%v\n CreateFromPaths=%v", aPaths, bPaths)
	}
}

func buildWithCreateFromDir(t *testing.T, src string) string {
	t.Helper()
	sqfs := filepath.Join(t.TempDir(), "fromdir.sqfs")
	out, err := os.Create(sqfs)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWriter(out, WithCompression(CompGzip))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.CreateFromDir(src); err != nil {
		t.Fatal(err)
	}
	out.Close()
	return sqfs
}

func buildWithCreateFromPaths(t *testing.T, src string, paths []string) string {
	t.Helper()
	sqfs := filepath.Join(t.TempDir(), "frompaths.sqfs")
	out, err := os.Create(sqfs)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWriter(out, WithCompression(CompGzip))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.CreateFromPaths(src, paths); err != nil {
		t.Fatal(err)
	}
	out.Close()
	return sqfs
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
