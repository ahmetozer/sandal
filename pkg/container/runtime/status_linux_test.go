//go:build linux

package runtime

import (
	"os"
	"strings"
	"testing"
)

// parseStartTime must read field 22 (starttime) of /proc/<pid>/stat robustly,
// even though field 2 (comm) is parenthesized and may contain spaces and
// parentheses. The parser locates the LAST ')' so comm content can't shift the
// field offsets.
func TestParseStartTime(t *testing.T) {
	toks := make([]string, 25)
	for i := range toks {
		toks[i] = "0"
	}
	toks[19] = "99999" // field 22 = starttime; index 19 in the post-comm fields
	// comm contains a space and an embedded ')'
	line := "1234 (od )dd) " + strings.Join(toks, " ") + "\n"

	got, err := parseStartTime(line)
	if err != nil {
		t.Fatalf("parseStartTime error: %v", err)
	}
	if got != 99999 {
		t.Fatalf("parseStartTime = %d, want 99999", got)
	}
}

func TestParseStartTimeMalformed(t *testing.T) {
	for _, bad := range []string{"", "no parens here", "123 (comm) S"} {
		if _, err := parseStartTime(bad); err == nil {
			t.Errorf("parseStartTime(%q) expected error, got nil", bad)
		}
	}
}

// IsPidRunningAs must verify identity: a live pid whose start-time differs from
// the expected one is a recycled, unrelated process and must read as not
// running. A wantStart of 0 degrades to a plain liveness check.
func TestIsPidRunningAsIdentity(t *testing.T) {
	self := os.Getpid()
	start, err := ProcessStartTime(self)
	if err != nil || start == 0 {
		t.Fatalf("ProcessStartTime(self) = %d, err %v", start, err)
	}

	if ok, err := IsPidRunningAs(self, start); err != nil || !ok {
		t.Fatalf("correct identity: ok=%v err=%v, want true", ok, err)
	}
	if ok, _ := IsPidRunningAs(self, start+1); ok {
		t.Fatal("mismatched start-time must report NOT running (recycled pid)")
	}
	if ok, err := IsPidRunningAs(self, 0); err != nil || !ok {
		t.Fatalf("unknown start (0): ok=%v err=%v, want true (liveness only)", ok, err)
	}
	if ok, _ := IsPidRunningAs(-1, 0); ok {
		t.Fatal("invalid pid must report NOT running")
	}
}

// ProcessStartTime of our own pid must be nonzero and stable across calls.
func TestProcessStartTimeSelfStable(t *testing.T) {
	a, err := ProcessStartTime(os.Getpid())
	if err != nil {
		t.Fatalf("ProcessStartTime(self) error: %v", err)
	}
	if a == 0 {
		t.Fatal("ProcessStartTime(self) = 0, want nonzero")
	}
	b, err := ProcessStartTime(os.Getpid())
	if err != nil || a != b {
		t.Fatalf("ProcessStartTime unstable: %d != %d (err %v)", a, b, err)
	}
}
