package cmd

import (
	"errors"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

func TestSetVMExitStatus(t *testing.T) {
	const pid = 4242
	panicErr := errors.New("guest kernel panicked or requested reboot")
	tests := []struct {
		name       string
		c          config.Config
		err        error
		wantUpdate bool
		wantStatus string
	}{
		{"clean exit of hosted vm", config.Config{VM: "kvm", HostPid: pid, Status: "running"}, nil, true, "exit 0"},
		{"guest panic of hosted vm", config.Config{VM: "kvm", HostPid: pid, Status: "running"}, panicErr, true, "err guest kernel panicked or requested reboot"},
		{"vm hosted by another process", config.Config{VM: "kvm", HostPid: pid + 1, Status: "running"}, panicErr, false, "running"},
		{"native container with same name", config.Config{HostPid: pid, Status: "running"}, nil, false, "running"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.c
			if got := setVMExitStatus(&c, pid, tt.err); got != tt.wantUpdate || c.Status != tt.wantStatus {
				t.Errorf("setVMExitStatus() = %v, status %q; want %v, %q", got, c.Status, tt.wantUpdate, tt.wantStatus)
			}
		})
	}
}
