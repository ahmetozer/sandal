//go:build linux

package squash

import (
	"archive/tar"
	"testing"
)

// TestPathIndexAppliesWhiteouts verifies the post-whiteout path set is
// computed correctly across multiple layers: file whiteouts remove the
// named target, opaque-dir markers clear every descendant from lower
// layers, and a later layer can re-add a whitened path.
func TestPathIndexAppliesWhiteouts(t *testing.T) {
	idx := newPathIndex()

	// Layer 0: add a, b, c (dir), c/d, c/e, c/sub, c/sub/deep
	for _, r := range []struct {
		name string
		typ  byte
	}{
		{"a", tar.TypeReg},
		{"b", tar.TypeReg},
		{"c", tar.TypeDir},
		{"c/d", tar.TypeReg},
		{"c/e", tar.TypeReg},
		{"c/sub", tar.TypeDir},
		{"c/sub/deep", tar.TypeReg},
	} {
		idx.record(r.name, r.typ)
	}

	// Layer 1: whiteout a, then re-add it; whiteout c/d.
	idx.record(".wh.a", tar.TypeReg)
	idx.record("a", tar.TypeReg)
	idx.record("c/.wh.d", tar.TypeReg)

	// Layer 2: opaque-dir on c/sub wipes c/sub/deep. Then add c/sub/shiny.
	idx.record("c/sub/.wh..wh..opq", tar.TypeReg)
	idx.record("c/sub/shiny", tar.TypeReg)

	got := idx.sortedPaths()
	want := []string{"a", "b", "c", "c/e", "c/sub", "c/sub/shiny"}
	if !equal(got, want) {
		t.Errorf("path set mismatch:\n got %v\nwant %v", got, want)
	}
}

func equal(a, b []string) bool {
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
