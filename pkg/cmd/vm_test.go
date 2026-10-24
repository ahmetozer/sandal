package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/forward"
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
	err := startVM("sandal-test-no-such-vm", vmconfig.VMConfig{}, nil) // no kernel: validation fails
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

// `sandal run -vm -d` boots through `sandal vm start`, which only has the
// saved VM config: the -p mappings must travel in it and reach the VMM, and
// the VMM must report readiness so the forking run can fail on a bind error.
func TestStartVMPassesForwards(t *testing.T) {
	stubRecordVMExit(t)
	kernel := filepath.Join(t.TempDir(), "Image")
	if err := os.WriteFile(kernel, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	pm, err := forward.ParseFlag("18081:8080")
	if err != nil {
		t.Fatal(err)
	}
	cfg := vmconfig.VMConfig{KernelPath: kernel, CPUCount: 1, MemoryBytes: vmconfig.MB,
		Forwards: []forward.PortMapping{pm}}

	var gotForwards []forward.PortMapping
	var gotReady func()
	prev := bootVM
	bootVM = func(name string, cfg vmconfig.VMConfig, forwards []forward.PortMapping, ready func()) error {
		gotForwards, gotReady = forwards, ready
		return nil
	}
	t.Cleanup(func() { bootVM = prev })

	report := func(error) {}
	if err := startVM("sandal-test-forwards", cfg, report); err != nil {
		t.Fatalf("startVM() = %v", err)
	}
	if len(gotForwards) != 1 || gotForwards[0].Raw != "18081:8080" {
		t.Fatalf("boot got forwards %+v, want the saved 18081:8080 mapping", gotForwards)
	}
	if gotReady == nil {
		t.Fatal("boot got no ready callback")
	}
}

// A VMM that fails before the VM runs reports the error on -ready-fd, so the
// forking `run -vm -d` fails with it instead of returning success.
func TestVMStartReportsFailureOnReadyFD(t *testing.T) {
	stubRecordVMExit(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	fd, err := syscall.Dup(int(w.Fd())) // vmStart owns and closes this one
	if err != nil {
		t.Fatal(err)
	}
	w.Close()

	runErr := vmStart([]string{"-name", "sandal-test-no-such-vm", "-ready-fd", strconv.Itoa(fd)})
	if runErr == nil {
		t.Fatal("vmStart succeeded without a saved config")
	}
	if err := vmconfig.WaitReady(r); err == nil || err.Error() != runErr.Error() {
		t.Fatalf("ready fd reported %v, want %v", err, runErr)
	}
}

// The forking `run -vm -d` waits on the ready fd with a timeout, so a
// failure must reach it before the exit is recorded: recording goes
// through the controller (or the daemon socket) and can be slow.
func TestVMStartReportsBeforeRecording(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	fd, err := syscall.Dup(int(w.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	w.Close()

	var reportedFirst error
	prev := recordVMExit
	recordVMExit = func(name string, err error) {
		reportedFirst = vmconfig.WaitReadyTimeout(r, 200*time.Millisecond)
	}
	t.Cleanup(func() { recordVMExit = prev })

	runErr := vmStart([]string{"-name", "sandal-test-no-such-vm", "-ready-fd", strconv.Itoa(fd)})
	if runErr == nil {
		t.Fatal("vmStart succeeded without a saved config")
	}
	if reportedFirst == nil || reportedFirst.Error() != runErr.Error() {
		t.Fatalf("when the exit was recorded the ready fd held %v, want the error %v", reportedFirst, runErr)
	}
}

// startVM reports a boot failure before it records the exit.
func TestStartVMReportsBeforeRecording(t *testing.T) {
	var events []string
	prev := recordVMExit
	recordVMExit = func(name string, err error) { events = append(events, "record") }
	t.Cleanup(func() { recordVMExit = prev })

	report := func(err error) {
		if err != nil {
			events = append(events, "report")
		}
	}
	if err := startVM("sandal-test-no-such-vm", vmconfig.VMConfig{}, report); err == nil {
		t.Fatal("startVM succeeded without a kernel")
	}
	if len(events) != 2 || events[0] != "report" || events[1] != "record" {
		t.Fatalf("events = %v, want [report record]", events)
	}
}
