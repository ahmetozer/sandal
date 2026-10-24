package config

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func TestReadyRoundTrip(t *testing.T) {
	bindErr := errors.New("port-forward setup failed for [18081:8080]: address already in use")
	tests := []struct {
		name    string
		report  error
		wantErr string
	}{
		{"ready", nil, ""},
		{"failed before ready", bindErr, bindErr.Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			ReportReady(nopWriteCloser{&buf}, tt.report)
			err := WaitReady(&buf)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("WaitReady() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("WaitReady() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// A VMM that exits (or crashes) without reporting must not count as ready.
func TestWaitReadyWithoutReport(t *testing.T) {
	err := WaitReady(strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "before it was ready") {
		t.Fatalf("WaitReady(empty) = %v, want an exited-before-ready error", err)
	}
}

// A VMM that neither reports nor exits must not block the caller forever.
func TestWaitReadyTimeout(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	start := time.Now()
	err := WaitReadyTimeout(r, 100*time.Millisecond)
	if !errors.Is(err, ErrReadyTimeout) {
		t.Fatalf("WaitReadyTimeout(silent) = %v, want ErrReadyTimeout", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("WaitReadyTimeout took %v with a 100ms timeout", took)
	}
}

func TestWaitReadyTimeoutReported(t *testing.T) {
	var buf bytes.Buffer
	ReportReady(nopWriteCloser{&buf}, nil)
	if err := WaitReadyTimeout(&buf, time.Second); err != nil {
		t.Fatalf("WaitReadyTimeout(ok) = %v, want nil", err)
	}
	buf.Reset()
	ReportReady(nopWriteCloser{&buf}, errors.New("bind: address already in use"))
	if err := WaitReadyTimeout(&buf, time.Second); err == nil || errors.Is(err, ErrReadyTimeout) {
		t.Fatalf("WaitReadyTimeout(failed) = %v, want the reported error", err)
	}
}
