//go:build linux

package sysctl

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Read reads a sysctl value (e.g. "net/ipv6/conf/eth0/accept_ra").
// Both dotted and slashed forms are accepted.
func Read(key string) (string, error) {
	path := "/proc/sys/" + strings.ReplaceAll(key, ".", "/")
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// Write sets a sysctl value.
func Write(key, value string) error {
	path := "/proc/sys/" + strings.ReplaceAll(key, ".", "/")
	return os.WriteFile(path, []byte(value), 0o644)
}

// Ensure reads `key`; if its value is not `want`, sets it. Logs at info
// when a change is made; logs at warn if the write fails. Returns the actual
// value after the attempt.
func Ensure(key, want string) (string, error) {
	current, err := Read(key)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", key, err)
	}
	if current == want {
		return current, nil
	}
	if err := Write(key, want); err != nil {
		slog.Warn("sysctl write failed", "key", key, "want", want, "current", current, "err", err)
		return current, err
	}
	slog.Info("sysctl set", "key", key, "from", current, "to", want)
	return want, nil
}
