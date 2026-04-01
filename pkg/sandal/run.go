package sandal

import "fmt"

// Run is the main entry point for `sandal run`.
// It handles platform dispatch (VM vs container) and flag parsing.
// CLI, daemon, and VM init all converge here.
func Run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no command option provided")
	}
	return platformRun(args)
}
