//go:build linux

package squashfs

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// TestWriterPreservesSpecialModeBits builds a squashfs from a tmpdir whose
// entries carry setuid/setgid/sticky/group-write bits and asserts the
// on-disk inode preserves them. Reads back via `unsquashfs -lln` and parses
// the octal mode column — no mount required.
func TestWriterPreservesSpecialModeBits(t *testing.T) {
	if _, err := exec.LookPath("unsquashfs"); err != nil {
		t.Skip("unsquashfs not installed")
	}

	src := t.TempDir()
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })

	mustWrite := func(rel string, body []byte, mode os.FileMode) {
		full := filepath.Join(src, rel)
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

	sqfs := filepath.Join(t.TempDir(), "out.sqfs")
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
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}

	// Read back the mode column via `unsquashfs -lln`. Output is `-mode uid/gid size date time path`.
	outBytes, err := exec.Command("unsquashfs", "-lln", sqfs).CombinedOutput()
	if err != nil {
		t.Fatalf("unsquashfs: %v\n%s", err, outBytes)
	}
	got := map[string]string{}
	for _, line := range splitLines(string(outBytes)) {
		fields := fieldsN(line, 6)
		if len(fields) < 6 {
			continue
		}
		got[fields[5]] = fields[0]
	}
	cases := []struct {
		path string
		want string
	}{
		{"squashfs-root/stickydir", "drwxrwxrwt"},
		{"squashfs-root/suid-bin", "-rwsr-xr-x"},
		{"squashfs-root/sgid-bin", "-rwxr-sr-x"},
		{"squashfs-root/grpwrite.sh", "-rw-rw-r--"},
	}
	for _, c := range cases {
		if got[c.path] != c.want {
			t.Errorf("%s: got %q, want %q", c.path, got[c.path], c.want)
		}
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func fieldsN(s string, n int) []string {
	out := make([]string, 0, n)
	i := 0
	for i < len(s) && len(out) < n-1 {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		start := i
		for i < len(s) && s[i] != ' ' && s[i] != '\t' {
			i++
		}
		if start < i {
			out = append(out, s[start:i])
		}
	}
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i < len(s) {
		out = append(out, s[i:])
	}
	return out
}
