package mgmt

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestExecStreamRoundTrip(t *testing.T) {
	for _, want := range []ExecExit{
		{},
		{Code: 3},
		{Code: 137, Signal: 9},
		{Code: 1, Error: `lookup nope in PATH: executable file not found`},
	} {
		var wire bytes.Buffer
		w := NewExecWriter(&wire)
		io.WriteString(w, "hello ")
		w.Write(nil) // an empty write sends nothing
		io.WriteString(w, "world\r\n")
		if err := w.Exit(want); err != nil {
			t.Fatalf("Exit: %v", err)
		}

		var out bytes.Buffer
		got, err := ReadExec(&wire, &out)
		if err != nil {
			t.Fatalf("ReadExec = %v", err)
		}
		if got != want {
			t.Errorf("exit = %+v, want %+v", got, want)
		}
		if out.String() != "hello world\r\n" {
			t.Errorf("output = %q", out.String())
		}
	}
}

// A stream that ends before the exit frame (guest gone, VM powered off) is
// an error, after the output that did arrive was copied.
func TestReadExecWithoutExit(t *testing.T) {
	var wire bytes.Buffer
	w := NewExecWriter(&wire)
	io.WriteString(w, "partial")
	for _, cut := range []int{wire.Len(), wire.Len() - 2, 3} {
		var out bytes.Buffer
		_, err := ReadExec(bytes.NewReader(wire.Bytes()[:cut]), &out)
		if err == nil {
			t.Fatalf("cut at %d: ReadExec = nil, want an error", cut)
		}
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("cut at %d: ReadExec = %v, want io.ErrUnexpectedEOF", cut, err)
		}
	}
	var out bytes.Buffer
	ReadExec(bytes.NewReader(wire.Bytes()), &out)
	if out.String() != "partial" {
		t.Errorf("output = %q, want partial", out.String())
	}
}

func TestReadExecRejectsUnknownFrame(t *testing.T) {
	_, err := ReadExec(strings.NewReader("?\x00\x00\x00\x00"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "unknown frame") {
		t.Fatalf("ReadExec = %v, want unknown frame error", err)
	}
}

func TestOffersExecUpgrade(t *testing.T) {
	cases := []struct {
		upgrade []string
		want    bool
	}{
		{nil, false},
		{[]string{"raw-stream"}, false},
		{[]string{ExecUpgrade}, true},
		{[]string{ExecUpgrade + ", raw-stream"}, true},
		{[]string{"raw-stream,  SANDAL-EXEC/1"}, true},
		{[]string{"raw-stream", ExecUpgrade}, true},
		{[]string{"sandal-exec/2"}, false},
	}
	for _, tc := range cases {
		h := http.Header{}
		for _, v := range tc.upgrade {
			h.Add("Upgrade", v)
		}
		if got := OffersExecUpgrade(h); got != tc.want {
			t.Errorf("OffersExecUpgrade(%q) = %v, want %v", tc.upgrade, got, tc.want)
		}
	}
}
