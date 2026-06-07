//go:build linux

package diskimage

import "testing"

func TestLoopReuseMatch(t *testing.T) {
	const f = "/img/klipper.sq"
	cases := []struct {
		name                            string
		haveFile                        string
		haveDeleted                     bool
		haveOff, haveSize               uint64
		wantFile                        string
		wantOff, wantSize               uint64
		match                           bool
	}{
		{"exact whole-file", f, false, 0, 0, f, 0, 0, true},
		{"exact partition", f, false, 1048576, 0, f, 1048576, 0, true},
		{"different offset (other partition)", f, false, 0, 0, f, 1048576, 0, false},
		{"deleted backing", f, true, 0, 0, f, 0, 0, false},
		{"different file", "/img/other.sq", false, 0, 0, f, 0, 0, false},
		{"different sizelimit", f, false, 0, 4096, f, 0, 0, false},
	}
	for _, tc := range cases {
		got := loopReuseMatch(tc.haveFile, tc.haveDeleted, tc.haveOff, tc.haveSize, tc.wantFile, tc.wantOff, tc.wantSize)
		if got != tc.match {
			t.Errorf("%s: loopReuseMatch = %v, want %v", tc.name, got, tc.match)
		}
	}
}

const sampleMounts = `/dev/loop1 /run/sandal/immutable/1 squashfs ro,relatime 0 0
/dev/loop2 /run/sandal/immutable/2 squashfs ro,relatime 0 0
/dev/loop3 /run/sandal/immutable/3 squashfs ro,relatime 0 0
overlay /run/sandal/rootfs/klipper overlay rw,relatime,lowerdir=/var/run/sandal/immutable/3,upperdir=/var/run/sandal/tmpfs/changes/klipper/upper,workdir=/var/run/sandal/tmpfs/changes/klipper/work 0 0
proc /proc proc rw,relatime 0 0
`

func TestParseImmutableMounts(t *testing.T) {
	got := parseImmutableMounts(sampleMounts, "/var/run/sandal/immutable")
	if len(got) != 3 {
		t.Fatalf("got %d immutable mounts, want 3: %v", len(got), got)
	}
	for _, no := range []int{1, 2, 3} {
		if _, ok := got[no]; !ok {
			t.Errorf("missing loop %d", no)
		}
	}
	// rootfs overlays and /proc must NOT be counted as immutable mounts.
	if len(got) != 3 {
		t.Errorf("unexpected extra entries: %v", got)
	}
}

func TestOverlayReferencesImmutable(t *testing.T) {
	// loop3 is referenced by klipper's overlay lowerdir (written as /var/run/...).
	if !overlayReferencesImmutable("/run/sandal/immutable/3", sampleMounts) {
		t.Error("immutable/3 should be reported in-use (klipper overlay lowerdir)")
	}
	// loop1/loop2 are mounted but no overlay references them -> not in use.
	if overlayReferencesImmutable("/run/sandal/immutable/1", sampleMounts) {
		t.Error("immutable/1 should NOT be reported in-use")
	}
	// querying with the /var/run form must also match (normalization).
	if !overlayReferencesImmutable("/var/run/sandal/immutable/3", sampleMounts) {
		t.Error("immutable/3 (/var/run form) should be reported in-use")
	}
}
