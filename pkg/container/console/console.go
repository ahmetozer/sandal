//go:build linux

package console

import (
	"path"

	"github.com/ahmetozer/sandal/pkg/env"
)

// Console mode constants
const (
	ModeFIFO   = "fifo"
	ModeSocket = "socket"
)

// Dir returns the console directory for a container.
func Dir(name string) string {
	return path.Join(env.RunDir, "console", name)
}

// StdinPath returns the stdin FIFO path.
func StdinPath(name string) string {
	return path.Join(Dir(name), "stdin")
}

// StdoutPath returns the stdout log file path.
func StdoutPath(name string) string {
	return path.Join(Dir(name), "stdout")
}

// StderrPath returns the stderr log file path.
func StderrPath(name string) string {
	return path.Join(Dir(name), "stderr")
}

// SocketPath returns the console unix socket path.
func SocketPath(name string) string {
	return path.Join(Dir(name), "console.sock")
}

// ModePath returns the path to the file indicating which console mode is active.
func ModePath(name string) string {
	return path.Join(Dir(name), "mode")
}
