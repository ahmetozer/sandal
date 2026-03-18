package squashfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewIncludeExcludeFilter(t *testing.T) {
	tests := []struct {
		name     string
		includes []string
		excludes []string
		path     string
		isDir    bool
		want     bool
	}{
		// Include-only cases
		{"include match file", []string{"/folder1"}, nil, "/folder1/file.txt", false, true},
		{"include match dir", []string{"/folder1"}, nil, "/folder1", true, true},
		{"include match nested", []string{"/folder1"}, nil, "/folder1/sub/deep.txt", false, true},
		{"include no match", []string{"/folder1"}, nil, "/folder2/file.txt", false, false},
		{"include ancestor dir traversed", []string{"/folder1/sub"}, nil, "/folder1", true, true},
		{"include ancestor non-dir rejected", []string{"/folder1/sub"}, nil, "/folder1", false, false},
		{"include multiple", []string{"/a", "/b"}, nil, "/b/x", false, true},

		// Exclude-only cases (includes default to ["/"])
		{"exclude match", []string{"/"}, []string{"/tmp"}, "/tmp/cache", false, false},
		{"exclude exact dir", []string{"/"}, []string{"/tmp"}, "/tmp", true, false},
		{"exclude no match", []string{"/"}, []string{"/tmp"}, "/etc/motd", false, true},
		{"exclude nested", []string{"/"}, []string{"/var/log"}, "/var/log/syslog", false, false},
		{"exclude sibling passes", []string{"/"}, []string{"/var/log"}, "/var/lib/data", false, true},

		// Include + exclude combined
		{"include+exclude: included file", []string{"/folder1"}, []string{"/folder1/tmp"}, "/folder1/data/f.txt", false, true},
		{"include+exclude: excluded subdir", []string{"/folder1"}, []string{"/folder1/tmp"}, "/folder1/tmp/cache", false, false},
		{"include+exclude: excluded exact", []string{"/folder1"}, []string{"/folder1/tmp"}, "/folder1/tmp", true, false},
		{"include+exclude: outside include", []string{"/folder1"}, []string{"/folder1/tmp"}, "/folder2/x", false, false},

		// Normalization
		{"trailing slash include", []string{"/folder1/"}, nil, "/folder1/x", false, true},
		{"trailing slash exclude", []string{"/"}, []string{"/tmp/"}, "/tmp/x", false, false},
		{"no leading slash", []string{"folder1"}, nil, "/folder1/x", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := NewIncludeExcludeFilter(tt.includes, tt.excludes)
			got := filter(tt.path, tt.isDir)
			if got != tt.want {
				t.Errorf("filter(%q, isDir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
			}
		})
	}
}

// TestCreateFromDirWithFilter creates a temp directory tree and verifies
// that the squashfs writer respects the path filter.
func TestCreateFromDirWithFilter(t *testing.T) {
	// Build test directory tree:
	//   /folder1/data/file.txt
	//   /folder1/tmp/cache.txt
	//   /folder2/folder2-1/sub.txt
	//   /folder3/other.txt
	root := t.TempDir()
	dirs := []string{
		"folder1/data",
		"folder1/tmp",
		"folder2/folder2-1",
		"folder3",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"folder1/data/file.txt":     "include-me",
		"folder1/tmp/cache.txt":     "exclude-me",
		"folder2/folder2-1/sub.txt": "include-sub",
		"folder3/other.txt":         "skip-folder3",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Create filtered squashfs: include /folder1 and /folder2/folder2-1, exclude /folder1/tmp
	outPath := filepath.Join(t.TempDir(), "filtered.sqfs")
	f, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	filter := NewIncludeExcludeFilter(
		[]string{"/folder1", "/folder2/folder2-1"},
		[]string{"/folder1/tmp"},
	)
	w, err := NewWriter(f, WithPathFilter(filter))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.CreateFromDir(root); err != nil {
		t.Fatal("CreateFromDir with filter:", err)
	}
	f.Close()

	// Verify the image is valid
	h, err := Info(outPath)
	if err != nil {
		t.Fatal("reading header:", err)
	}
	if h.Magic != SQUASHFS_MAGIC {
		t.Fatalf("wrong magic: 0x%x", h.Magic)
	}

	// Count inodes: root + folder1 + data + file.txt + folder2 + folder2-1 + sub.txt = 7
	// folder1/tmp, folder3, and their contents should be excluded
	// folder2 is included as an ancestor of folder2/folder2-1
	t.Logf("Filtered squashfs: %d inodes", h.Inodes)

	// folder1/tmp/cache.txt and folder3/other.txt should NOT be present (4 inodes excluded: folder1/tmp, cache.txt, folder3, other.txt)
	// Expected: root(1) + folder1(1) + data(1) + file.txt(1) + folder2(1) + folder2-1(1) + sub.txt(1) = 7
	if h.Inodes != 7 {
		t.Errorf("expected 7 inodes (filtered), got %d", h.Inodes)
	}
}

// TestCreateFromDirExcludeOnly verifies exclude-only mode (no includes = include everything).
func TestCreateFromDirExcludeOnly(t *testing.T) {
	root := t.TempDir()
	dirs := []string{"keep", "skip/nested"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"keep/a.txt":        "kept",
		"skip/nested/b.txt": "skipped",
		"root.txt":          "kept-root",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	outPath := filepath.Join(t.TempDir(), "excl.sqfs")
	f, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	filter := NewIncludeExcludeFilter([]string{"/"}, []string{"/skip"})
	w, err := NewWriter(f, WithPathFilter(filter))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.CreateFromDir(root); err != nil {
		t.Fatal("CreateFromDir exclude-only:", err)
	}
	f.Close()

	h, err := Info(outPath)
	if err != nil {
		t.Fatal("reading header:", err)
	}

	// Expected: root(1) + keep(1) + a.txt(1) + root.txt(1) = 4
	// Excluded: skip(1) + nested(1) + b.txt(1) = 3
	t.Logf("Exclude-only squashfs: %d inodes", h.Inodes)
	if h.Inodes != 4 {
		t.Errorf("expected 4 inodes (exclude-only), got %d", h.Inodes)
	}
}

// TestCreateFromDirNoFilter verifies that without a filter, all entries are included.
func TestCreateFromDirNoFilter(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"a", "b"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	for _, name := range []string{"a/1.txt", "b/2.txt", "c.txt"} {
		os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644)
	}

	outPath := filepath.Join(t.TempDir(), "all.sqfs")
	f, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	w, err := NewWriter(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.CreateFromDir(root); err != nil {
		t.Fatal("CreateFromDir no filter:", err)
	}
	f.Close()

	h, err := Info(outPath)
	if err != nil {
		t.Fatal("reading header:", err)
	}

	// root(1) + a(1) + 1.txt(1) + b(1) + 2.txt(1) + c.txt(1) = 6
	if h.Inodes != 6 {
		t.Errorf("expected 6 inodes (no filter), got %d", h.Inodes)
	}
}
