package cmd

import (
	"errors"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
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

// stubRecordVMExit captures the errors recorded by startVM/vmStart.
func stubRecordVMExit(t *testing.T) *[]error {
	t.Helper()
	var got []error
	prev := recordVMExit
	recordVMExit = func(name string, err error) { got = append(got, err) }
	t.Cleanup(func() { recordVMExit = prev })
	return &got
}

func TestStartVMRecordsEarlyFailure(t *testing.T) {
	got := stubRecordVMExit(t)
	err := startVM("sandal-test-no-such-vm", vmconfig.VMConfig{}) // no kernel: validation fails
	if err == nil {
		t.Fatal("startVM succeeded without a kernel")
	}
	if len(*got) != 1 || (*got)[0] != err {
		t.Fatalf("recorded %v, want exactly the returned error %v", *got, err)
	}
}

func TestVMStartRecordsConfigLoadFailure(t *testing.T) {
	got := stubRecordVMExit(t)
	err := vmStart([]string{"-name", "sandal-test-no-such-vm"})
	if err == nil {
		t.Fatal("vmStart succeeded without a saved config")
	}
	if len(*got) != 1 || (*got)[0] != err {
		t.Fatalf("recorded %v, want exactly the returned error %v", *got, err)
	}
}
