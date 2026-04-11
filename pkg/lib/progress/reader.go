package progress

import (
	"io"
	"time"
)

// Reader wraps an io.Reader, counting bytes read and sending progress
// Events to a channel at most every 500ms. If ch is nil, it acts as a
// zero-cost passthrough.
type Reader struct {
	r        io.Reader
	ch       chan<- Event
	phase    Phase
	taskID   string
	total    int64
	current  int64
	lastSend time.Time
}

// NewReader wraps r with progress reporting.
func NewReader(r io.Reader, ch chan<- Event, phase Phase, taskID string, total int64) *Reader {
	return &Reader{
		r:      r,
		ch:     ch,
		phase:  phase,
		taskID: taskID,
		total:  total,
	}
}

func (r *Reader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.current += int64(n)

	if r.ch != nil {
		now := time.Now()
		if now.Sub(r.lastSend) >= 500*time.Millisecond {
			r.lastSend = now
			r.send(false)
		}
		if err == io.EOF {
			r.send(true)
		}
	}

	return n, err
}

func (r *Reader) send(done bool) {
	select {
	case r.ch <- Event{
		Phase:   r.phase,
		TaskID:  r.taskID,
		Current: r.current,
		Total:   r.total,
		Done:    done,
	}:
	default:
	}
}

// readCloser wraps a Reader and preserves the underlying Close.
type readCloser struct {
	*Reader
	closer io.Closer
}

func (rc *readCloser) Close() error {
	return rc.closer.Close()
}

// NewReadCloser wraps rc with progress reporting, preserving Close.
func NewReadCloser(rc io.ReadCloser, ch chan<- Event, phase Phase, taskID string, total int64) io.ReadCloser {
	return &readCloser{
		Reader: NewReader(rc, ch, phase, taskID, total),
		closer: rc,
	}
}
