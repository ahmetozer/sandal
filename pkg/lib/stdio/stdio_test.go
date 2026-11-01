package stdio

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestStreamsFallback(t *testing.T) {
	var nilStreams *Streams
	for name, s := range map[string]*Streams{"nil": nilStreams, "empty": {}} {
		if s.In() != os.Stdin {
			t.Errorf("%s: In() is not os.Stdin", name)
		}
		if s.Out() != os.Stdout {
			t.Errorf("%s: Out() is not os.Stdout", name)
		}
		if s.Err() != os.Stderr {
			t.Errorf("%s: Err() is not os.Stderr", name)
		}
	}
}

func TestStreamsCaller(t *testing.T) {
	in := strings.NewReader("in")
	var out, errOut bytes.Buffer
	s := &Streams{Stdin: in, Stdout: &out, Stderr: &errOut}
	if s.In() != in {
		t.Error("In() is not the caller's stdin")
	}
	if s.Out() != &out {
		t.Error("Out() is not the caller's stdout")
	}
	if s.Err() != &errOut {
		t.Error("Err() is not the caller's stderr")
	}

	// Only the nil field falls back.
	s = &Streams{Stdout: &out}
	if s.In() != os.Stdin || s.Out() != &out || s.Err() != os.Stderr {
		t.Error("a partly set Streams does not fall back per field")
	}
}
