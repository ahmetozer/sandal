//go:build linux

package sandal

import (
	"errors"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

func TestSetForkedVMExit(t *testing.T) {
	const pid = 4242
	exitErr := errors.New("exit status 1")
	tests := []struct {
		name       string
		latest     config.Config
		err        error
		wantUpdate bool
		wantStatus string
	}{
		{"vmm exited cleanly without recording", config.Config{HostPid: pid, Status: "running"}, nil, true, "exit 0"},
		{"vmm failed without recording", config.Config{HostPid: pid, Status: "running"}, exitErr, true, "err exit status 1"},
		{"vmm recorded the panic", config.Config{HostPid: pid, Status: "err guest kernel panicked or requested reboot"}, exitErr, false, "err guest kernel panicked or requested reboot"},
		{"vmm recorded a clean exit", config.Config{HostPid: pid, Status: "exit 0"}, nil, false, "exit 0"},
		{"stopped by the user", config.Config{HostPid: pid, Status: "stop"}, exitErr, false, "stop"},
		{"record belongs to a newer run", config.Config{HostPid: pid + 1, Status: "running"}, exitErr, false, "running"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.latest
			if got := setForkedVMExit(&c, pid, tt.err); got != tt.wantUpdate || c.Status != tt.wantStatus {
				t.Errorf("setForkedVMExit() = %v, status %q; want %v, %q", got, c.Status, tt.wantUpdate, tt.wantStatus)
			}
		})
	}
}
