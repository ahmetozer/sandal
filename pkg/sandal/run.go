package sandal

import (
	"fmt"

	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
)

// ExitError is returned by Run when the foreground container ran and
// exited with a non-zero code, and by Exec when the command did (Code;
// 128+signal when a signal ended it, with Signal set). The CLI exits with
// that code.
type ExitError = crt.ExitError

// Run is the main entry point for `sandal run`.
// It handles platform dispatch (VM vs container) and flag parsing.
// CLI, daemon, and VM init all converge here.
func Run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no command option provided")
	}
	return platformRun(args)
}
