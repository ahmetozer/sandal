//go:build linux || darwin

package clean

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmetozer/sandal/pkg/env"
)

func withLibDir(t *testing.T, dir string) {
	t.Helper()
	prev := env.LibDir
	env.LibDir = dir
	t.Cleanup(func() { env.LibDir = prev })
}

func TestIsInsideLibDir(t *testing.T) {
	tmp := t.TempDir()
	libDir := filepath.Join(tmp, "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	withLibDir(t, libDir)

	// A plain file inside LibDir.
	inside := filepath.Join(libDir, "changedir", "foo.img")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(tmp, "not-sandal", "foo.img")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		target string
		want   bool
	}{
		{"inside", inside, true},
		{"outside sibling", outside, false},
		{"libdir itself", libDir, false},
		{"empty", "", false},
		{"dotdot escape", filepath.Join(libDir, "..", "not-sandal", "foo.img"), false},
		{"nonexistent but inside", filepath.Join(libDir, "ghost.img"), true},
		{"nonexistent outside", filepath.Join(tmp, "nowhere", "ghost.img"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := IsInsideLibDir(tc.target)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Errorf("IsInsideLibDir(%q) = %v, want %v", tc.target, got, tc.want)
			}
		})
	}
}

// Regression: on hosts where /var/run -> /run, env.RunDir may be
// "/var/run/sandal" while the resolved form is "/run/sandal". A
// nonexistent target like "/var/run/sandal/rootfs/mytest" used to be
// rejected because EvalSymlinks failed on the full path and we fell
// back to the unresolved lexical form, which didn't share the
// resolved prefix. IsInsideLibDir must resolve via the nearest
// existing ancestor.
func TestIsInsideLibDir_SymlinkedRootNonexistentTarget(t *testing.T) {
	tmp := t.TempDir()
	realLib := filepath.Join(tmp, "real-lib")
	if err := os.MkdirAll(realLib, 0o755); err != nil {
		t.Fatal(err)
	}
	// symlinked-lib -> real-lib (mirrors /var/run -> /run)
	linkLib := filepath.Join(tmp, "symlinked-lib")
	if err := os.Symlink(realLib, linkLib); err != nil {
		t.Fatal(err)
	}
	withLibDir(t, linkLib)

	// A path inside linkLib where the leaf does not yet/no longer
	// exists (its parent dir does though).
	if err := os.MkdirAll(filepath.Join(linkLib, "rootfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	ghost := filepath.Join(linkLib, "rootfs", "gone")

	ok, err := IsInsideLibDir(ghost)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !ok {
		t.Errorf("nonexistent target inside symlinked LibDir was rejected")
	}
}

func TestIsInsideLibDir_SymlinkEscape(t *testing.T) {
	tmp := t.TempDir()
	libDir := filepath.Join(tmp, "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(tmp, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	// Symlink lib/escape -> ../elsewhere. A path that walks through
	// the symlink should resolve outside LibDir.
	link := filepath.Join(libDir, "escape")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(elsewhere, "victim")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	withLibDir(t, libDir)

	viaSymlink := filepath.Join(link, "victim")
	got, err := IsInsideLibDir(viaSymlink)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got {
		t.Errorf("symlink-escaped path unexpectedly reported as inside LibDir")
	}
}
