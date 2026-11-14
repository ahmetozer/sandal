package sandal

import (
	"context"
	"errors"
	"testing"
)

func TestRunResult(t *testing.T) {
	exit5 := &ExitError{Code: 5}
	other := errors.New("mount failed")
	live := context.Background()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want error
	}{
		{"exit 0", live, nil, nil},
		{"non-zero exit", live, exit5, exit5},
		{"sandal error", live, other, other},
		{"killed by cancel", cancelled, &ExitError{Code: -1}, context.Canceled},
		{"exit 0 despite cancel", cancelled, nil, nil},
		{"sandal error after cancel", cancelled, other, other},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runResult(tt.ctx, tt.err); got != tt.want {
				t.Errorf("runResult = %v, want %v", got, tt.want)
			}
		})
	}
}

// A cancelled ctx stops the run before anything is set up.
func TestRunContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RunContext(ctx, []string{"-lw", "/", "--", "true"}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunContext = %v, want context.Canceled", err)
	}
}
