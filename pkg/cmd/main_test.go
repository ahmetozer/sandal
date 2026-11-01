package cmd

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/ahmetozer/sandal/pkg/sandal"
)

// A foreground container's non-zero exit becomes the process exit code;
// sandal's own errors stay 1.
func TestExecuteSubCommandExitCode(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		start int // exitCode already chosen by the subcommand
		want  int
	}{
		{"success", nil, 0, 0},
		{"container exit 5", &sandal.ExitError{Code: 5}, 0, 5},
		{"wrapped container exit", fmt.Errorf("rerun: %w", &sandal.ExitError{Code: 3}), 0, 3},
		{"signalled container", &sandal.ExitError{Code: -1}, 0, -1},
		{"sandal error", errors.New("unknown flag"), 0, 1},
		{"sandal error keeps chosen code", errors.New("failed"), 2, 2},
	}
	savedArgs, savedHandler, savedCode := os.Args, ExitHandler, exitCode
	t.Cleanup(func() { os.Args, ExitHandler, exitCode = savedArgs, savedHandler, savedCode })
	os.Args = []string{"sandal", "run"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exitCode = tt.start
			got := -100
			ExitHandler = func(code int) { got = code }
			executeSubCommand(func([]string) error { return tt.err })
			if got != tt.want {
				t.Errorf("exit code = %d, want %d", got, tt.want)
			}
		})
	}
}
