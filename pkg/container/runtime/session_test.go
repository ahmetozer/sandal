package runtime

import (
	"bytes"
	"context"
	"testing"

	"github.com/ahmetozer/sandal/pkg/lib/stdio"
)

// A nil session is the CLI's: no caller streams, a context that never
// ends, and no placement callback.
func TestRunSessionNil(t *testing.T) {
	var s *RunSession
	if s.Context() != context.Background() {
		t.Error("nil session: Context() is not context.Background()")
	}
	if s.Streams() != nil {
		t.Error("nil session: Streams() is not nil")
	}
	s.Placed() // must not panic

	if (&RunSession{}).Context() != context.Background() {
		t.Error("session without Ctx: Context() is not context.Background()")
	}
}

func TestRunSessionCaller(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	rio := &stdio.Streams{Stdout: &out}
	placed := 0
	s := &RunSession{Ctx: ctx, IO: rio, OnPlaced: func() { placed++ }}

	if s.Context() != ctx {
		t.Error("Context() is not Ctx")
	}
	if s.Streams() != rio {
		t.Error("Streams() is not IO")
	}
	s.Placed()
	if placed != 1 {
		t.Errorf("OnPlaced called %d times, want 1", placed)
	}
}

// WithOnPlaced adds a callback to a copy: the caller's session is not
// changed, and its own callback still runs.
func TestRunSessionWithOnPlaced(t *testing.T) {
	var calls []string
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rio := &stdio.Streams{}
	s := &RunSession{Ctx: ctx, IO: rio, OnPlaced: func() { calls = append(calls, "caller") }}

	w := s.WithOnPlaced(func() { calls = append(calls, "added") })
	if w == s {
		t.Fatal("WithOnPlaced returned the caller's session")
	}
	if w.Ctx != ctx || w.IO != rio {
		t.Error("WithOnPlaced dropped Ctx or IO")
	}
	w.Placed()
	if len(calls) != 2 || calls[0] != "added" || calls[1] != "caller" {
		t.Errorf("calls = %v, want [added caller]", calls)
	}

	calls = nil
	s.Placed()
	if len(calls) != 1 || calls[0] != "caller" {
		t.Errorf("caller's session changed: calls = %v, want [caller]", calls)
	}

	// From a nil (CLI) session.
	calls = nil
	var cli *RunSession
	w = cli.WithOnPlaced(func() { calls = append(calls, "added") })
	if w.Streams() != nil || w.Context() != context.Background() {
		t.Error("WithOnPlaced on nil: not a CLI session")
	}
	w.Placed()
	if len(calls) != 1 {
		t.Errorf("calls = %v, want [added]", calls)
	}
}
