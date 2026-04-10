//go:build !linux

package progress

import "os"

func isTerminal(_ *os.File) bool {
	return false
}

func terminalWidth(_ *os.File) int {
	return 80
}
