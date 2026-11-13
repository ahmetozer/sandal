package mgmt

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// ExecUpgrade is the Upgrade protocol of a POST /exec session in which the
// guest frames what it sends, so that the command's exit status can follow
// its output. The host offers it ahead of the plain "raw-stream"; a guest
// that predates it answers raw-stream and sends bare output without a
// status. The host → guest direction (stdin) is raw in both.
const ExecUpgrade = "sandal-exec/1"

// ExecExit ends a framed exec stream: how the command ended (Code, in the
// shell convention, with Signal set when a signal ended it), or Error when
// the guest could not run it.
type ExecExit struct {
	Code   int    `json:"code"`
	Signal int    `json:"signal,omitempty"`
	Error  string `json:"error,omitempty"`
}

// A frame is a kind byte, the payload length as a big-endian uint32 and the
// payload: output bytes, or the ExecExit as JSON in the last frame.
const (
	frameOutput = 'o'
	frameExit   = 'x'

	maxOutputFrame = 64 << 10
	maxExitFrame   = 64 << 10
)

// OffersExecUpgrade reports whether the Upgrade header of an exec request
// lists ExecUpgrade.
func OffersExecUpgrade(h http.Header) bool {
	for _, v := range h.Values("Upgrade") {
		for _, proto := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(proto), ExecUpgrade) {
				return true
			}
		}
	}
	return false
}

// ExecWriter writes the guest side of a framed exec stream: each Write is
// sent as output, Exit as the final frame. It is safe for concurrent use.
type ExecWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewExecWriter returns an ExecWriter that writes frames to w.
func NewExecWriter(w io.Writer) *ExecWriter {
	return &ExecWriter{w: w}
}

// Write sends p as output.
func (e *ExecWriter) Write(p []byte) (n int, err error) {
	for len(p) > 0 {
		chunk := p[:min(len(p), maxOutputFrame)]
		if err := e.frame(frameOutput, chunk); err != nil {
			return n, err
		}
		n += len(chunk)
		p = p[len(chunk):]
	}
	return n, nil
}

// Exit sends the exit frame; nothing may be written after it.
func (e *ExecWriter) Exit(x ExecExit) error {
	payload, err := json.Marshal(x)
	if err != nil {
		return err
	}
	return e.frame(frameExit, payload)
}

// frame writes one frame with a single Write, so frames never interleave
// and a keystroke echo stays one packet on the vsock.
func (e *ExecWriter) frame(kind byte, payload []byte) error {
	buf := make([]byte, 5+len(payload))
	buf[0] = kind
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[5:], payload)

	e.mu.Lock()
	defer e.mu.Unlock()
	_, err := e.w.Write(buf)
	return err
}

// ReadExec reads the host side of a framed exec stream: it copies the
// output to out until the exit frame and returns it. A stream that ends
// before the exit frame returns io.ErrUnexpectedEOF.
func ReadExec(r io.Reader, out io.Writer) (ExecExit, error) {
	var hdr [5]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return ExecExit{}, unexpectedEOF(err)
		}
		n := binary.BigEndian.Uint32(hdr[1:])
		switch hdr[0] {
		case frameOutput:
			if _, err := io.CopyN(out, r, int64(n)); err != nil {
				return ExecExit{}, unexpectedEOF(err)
			}
		case frameExit:
			if n > maxExitFrame {
				return ExecExit{}, fmt.Errorf("exec stream: exit frame of %d bytes", n)
			}
			payload := make([]byte, n)
			if _, err := io.ReadFull(r, payload); err != nil {
				return ExecExit{}, unexpectedEOF(err)
			}
			var x ExecExit
			if err := json.Unmarshal(payload, &x); err != nil {
				return ExecExit{}, fmt.Errorf("exec stream: exit frame: %w", err)
			}
			return x, nil
		default:
			return ExecExit{}, fmt.Errorf("exec stream: unknown frame %q", hdr[0])
		}
	}
}

// unexpectedEOF reports a stream that ended at a frame boundary as cut
// short too: only the exit frame may end it.
func unexpectedEOF(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}
