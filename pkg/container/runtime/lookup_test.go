package runtime

import (
	"os"
	"strings"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

func TestContPidLookup(t *testing.T) {
	self := os.Getpid()
	start, err := ProcessStartTime(self)
	if err != nil {
		t.Fatalf("ProcessStartTime(self) = %v", err)
	}
	conts := []*config.Config{
		{Name: "up", ContPid: self, ContPidStart: start},
		{Name: "stopped", ContPid: 0},
		// A recorded pid that now belongs to another process.
		{Name: "recycled", ContPid: self, ContPidStart: start + 1},
		{Name: "vm", VM: "kvm", HostPid: self},
	}
	lookup := ContPidLookup(conts)

	if pid, err := lookup("up"); err != nil || pid != self {
		t.Fatalf("lookup(up) = %d, %v; want %d, nil", pid, err, self)
	}
	for name, want := range map[string]string{
		"stopped":  "not running",
		"recycled": "not running",
		"vm":       "runs in a VM",
		"nope":     "not found",
	} {
		if _, err := lookup(name); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("lookup(%s) error = %v, want it to contain %q", name, err, want)
		}
	}
}
