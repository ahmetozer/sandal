package runtime

import (
	"fmt"
	"os"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/controller"
)

const (
	ContainerStatusCreating = "creating"
	ContainerStatusRunning  = "running"
	ContainerStatusStopped  = "stopped"
	ContainerStatusHang     = "hang"
)

func IsContainerRunning(name string) (bool, error) {
	oldConfig, err := controller.GetContainer(name)
	if err == nil {
		pid, wantStart := oldConfig.MonitorPidIdentity()
		b, err := IsPidRunningAs(pid, wantStart)
		if err != nil && pid != 0 {
			return false, fmt.Errorf("unable to check pid %d: %v", pid, err)
		}
		return b, nil
	}
	return false, nil
}

func SendSig(pid, sig int) error {
	return syscall.Kill(pid, syscall.Signal(sig))
}

// ProcessStartTime returns the kernel start-time identity for pid (0 on
// platforms with no such notion). Pairing pid with start-time makes liveness
// and kill checks immune to PID reuse: a recycled pid has a different
// start-time. Returns 0 for non-positive pids.
func ProcessStartTime(pid int) (uint64, error) {
	if pid <= 0 {
		return 0, nil
	}
	return processStartTime(pid)
}

// IsPidRunningAs reports whether pid is alive AND still the process we expect,
// by comparing its start-time to wantStart. When wantStart is 0 (identity
// unknown — e.g. a not-yet-migrated record, or a platform without start-time)
// it degrades to a plain liveness check. A live pid whose start-time differs
// from wantStart is treated as NOT running: it's a recycled, unrelated process.
func IsPidRunningAs(pid int, wantStart uint64) (bool, error) {
	alive, err := IsPidRunning(pid)
	if err != nil || !alive {
		return alive, err
	}
	if wantStart == 0 {
		return true, nil
	}
	gotStart, sErr := ProcessStartTime(pid)
	if sErr != nil {
		// Can't read start-time for a pid we just saw alive: be conservative
		// and report alive so we never kill/duplicate based on a transient
		// read failure.
		return true, nil
	}
	if gotStart != wantStart {
		return false, nil
	}
	return true, nil
}

var ErrPidExistenceControl = fmt.Errorf("unable to find process")

func IsPidRunning(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, ErrPidExistenceControl
		}
		if err == os.ErrProcessDone {
			return false, nil
		}
		return false, err
	}
	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return isPidAlive(pid)
	}
	if err == os.ErrProcessDone {
		return false, nil
	}
	return false, err
}
