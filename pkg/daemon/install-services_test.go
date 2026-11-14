//go:build linux

package daemon

import (
	"bufio"
	"bytes"
	"testing"
)

// Containers the daemon starts live in its service cgroup. With systemd's
// default KillMode=control-group, `systemctl restart sandal` would kill
// them all, which a daemon restart must not do.
func TestSystemdUnitStopsOnlyTheDaemon(t *testing.T) {
	s := bufio.NewScanner(bytes.NewReader(systemdSandalService))
	for s.Scan() {
		if s.Text() == "KillMode=process" {
			return
		}
	}
	t.Fatalf("sandal.service has no KillMode=process:\n%s", systemdSandalService)
}
