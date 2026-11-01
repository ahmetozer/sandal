//go:build linux || darwin

package sandal

import (
	"os"
	"testing"
)

// The TTY auto-detection looks at os.Stdin only for the CLI: an in-process
// caller's run gets a TTY only from -t, even when the process's stdin is a
// terminal.
func TestStdinIsTerminal(t *testing.T) {
	// /dev/null is a character device, as a terminal is to the check.
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	saved := os.Stdin
	os.Stdin = null
	defer func() { os.Stdin = saved }()

	if !stdinIsTerminal(nil) {
		t.Error("CLI run (nil RunIO) on a character-device stdin: no TTY detected")
	}
	if stdinIsTerminal(&RunIO{}) {
		t.Error("in-process run (RunIO set) detected a TTY from os.Stdin")
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	os.Stdin = pr
	if stdinIsTerminal(nil) {
		t.Error("CLI run on a pipe stdin detected a TTY")
	}
}

func TestRunContextNoArgs(t *testing.T) {
	if err := RunContext(t.Context(), nil, &RunIO{}); err == nil {
		t.Fatal("RunContext without args returned no error")
	}
}
