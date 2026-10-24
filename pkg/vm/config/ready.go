package config

import (
	"errors"
	"io"
	"strings"
	"time"
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

// ErrReadyTimeout is returned by WaitReadyTimeout when the VMM neither
// reported nor exited in time.
var ErrReadyTimeout = errors.New("VM process did not report ready in time")

// WaitReadyTimeout is WaitReady that gives up after timeout with
// ErrReadyTimeout. The read keeps running in the background until r ends
// (the VMM reports or exits), so the caller should stop the VMM then.
func WaitReadyTimeout(r io.Reader, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- WaitReady(r) }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return ErrReadyTimeout
	}
}
