//go:build linux

package loopdev

import "testing"

// parseBackingFile must clean the path and report whether the kernel marked the
// backing file "(deleted)" (i.e. it was replaced/unlinked since attach), which
// the reuse lookup uses to refuse reusing a stale mount.
func TestParseBackingFile(t *testing.T) {
	cases := []struct {
		raw         string
		wantPath    string
		wantDeleted bool
	}{
		{"/media/mmcblk0p1/sandal/image/klipper.sq", "/media/mmcblk0p1/sandal/image/klipper.sq", false},
		{"/img/klipper.sq (deleted)", "/img/klipper.sq", true},
		{"/img/./a/../klipper.sq", "/img/klipper.sq", false},
		{"  /img/x.sq  ", "/img/x.sq", false},
		{"", "", false},
		{"   ", "", false},
	}
	for _, tc := range cases {
		path, deleted := parseBackingFile(tc.raw)
		if path != tc.wantPath || deleted != tc.wantDeleted {
			t.Errorf("parseBackingFile(%q) = (%q,%v), want (%q,%v)", tc.raw, path, deleted, tc.wantPath, tc.wantDeleted)
		}
	}
}
