//go:build linux || darwin

package sandal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckGuestBuildOutput(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "app_latest.sqfs")
	write := func(data string) {
		t.Helper()
		// Same as the guest builder: write a temp file, then rename it over
		// the tagged path.
		tmp := filepath.Join(dir, ".build.sqfs.tmp")
		if err := os.WriteFile(tmp, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, out); err != nil {
			t.Fatal(err)
		}
	}

	// No previous image, guest wrote nothing: failure.
	if err := checkGuestBuildOutput(out, nil); err == nil {
		t.Error("missing image: want error, got nil")
	}

	// No previous image, guest wrote one: success.
	write("new")
	if err := checkGuestBuildOutput(out, nil); err != nil {
		t.Errorf("new image: %v", err)
	}

	// A previous image exists and the guest did not replace it: failure,
	// even though a file is present at the tagged path.
	before, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkGuestBuildOutput(out, before); err == nil {
		t.Error("stale image: want error, got nil")
	}

	// A previous image exists and the guest replaced it: success.
	write("rebuilt")
	if err := checkGuestBuildOutput(out, before); err != nil {
		t.Errorf("rebuilt image: %v", err)
	}
}
