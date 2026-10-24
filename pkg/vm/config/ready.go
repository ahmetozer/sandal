package config

import (
	"errors"
	"io"
	"strings"
)

// readyOK is what a VMM writes on its ready pipe once the VM is started and
// its port forwards are listening. Anything else written is the error that
// stopped it before that point.
const readyOK = "ok"

// ReportReady tells the sandal that forked this VMM (`run -vm -d`, daemon)
// whether the VM came up: nil once it is running with its forwards bound,
// otherwise the error that stopped it. It closes w, so call it once.
func ReportReady(w io.WriteCloser, err error) {
	msg := readyOK
	if err != nil {
		msg = err.Error()
	}
	io.WriteString(w, msg)
	w.Close()
}

// WaitReady reads the VMM's ReportReady message from r until EOF. It returns
// nil when the VM is ready, the reported error when it failed, and an error
// when the VMM exited without reporting.
func WaitReady(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	msg := strings.TrimSpace(string(data))
	switch msg {
	case readyOK:
		return nil
	case "":
		return errors.New("VM process exited before it was ready")
	}
	return errors.New(msg)
}
