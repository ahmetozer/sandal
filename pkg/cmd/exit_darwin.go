//go:build darwin

package cmd

import "os"

func init() {
	// Main records a failed subcommand in exitCode but only acts on it
	// through ExitHandler. Without one, Main returns and the process
	// exits 0 even when the command failed.
	ExitHandler = os.Exit
}
