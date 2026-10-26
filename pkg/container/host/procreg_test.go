//go:build linux

package host

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

// withRecord makes controller.GetContainer return rec, and points the cgroup
// sweep at an empty temp dir so tests never touch /sys/fs/cgroup.
func withRecord(t *testing.T, rec *config.Config) string {
	t.Helper()
	prevContainers := controller.Containers
	controller.Containers = func() ([]*config.Config, error) {
		return []*config.Config{rec.Clone()}, nil
	}
	prevBase := strayCgroupBase
	strayCgroupBase = t.TempDir()
	t.Cleanup(func() {
		controller.Containers = prevContainers
		strayCgroupBase = prevBase
		procs.reset()
	})
	return strayCgroupBase
}

// startSleep starts `sleep 300` and reaps it in the background, as the
// daemon's wait goroutine does. The returned channel closes once it exited.
func startSleep(t *testing.T) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	t.Cleanup(func() { cmd.Process.Kill(); <-done })
	return cmd, done
}

// deadPid returns the pid of a process that already exited and was reaped.
func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func startTime(t *testing.T, pid int) uint64 {
	t.Helper()
	st, err := crt.ProcessStartTime(pid)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func assertExited(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("stray was not killed")
	}
}

func assertAlive(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
		t.Fatal("process was killed but must not be")
	case <-time.After(300 * time.Millisecond):
	}
}

// The record lost the real pid (it points at a dead one) while the instance
// the daemon started under that name is still running: it must be killed.
func TestKillStraysKillsUntrackedInstance(t *testing.T) {
	withRecord(t, &config.Config{Name: "web", ContPid: deadPid(t), ContPidStart: 1})
	cmd, done := startSleep(t)
	procs.Add("web", cmd.Process.Pid)

	KillStrays("web")
	assertExited(t, done)
}

// The registered pid exited and its number now belongs to an unrelated
// process (different start-time): it must not be signalled.
func TestKillStraysSkipsReusedPid(t *testing.T) {
	withRecord(t, &config.Config{Name: "web", ContPid: deadPid(t), ContPidStart: 1})
	cmd, done := startSleep(t)
	procs.add("web", cmd.Process.Pid, startTime(t, cmd.Process.Pid)+1)

	KillStrays("web")
	assertAlive(t, done)
}

// Other names' instances are not strays of "web".
func TestKillStraysOnlyMatchesName(t *testing.T) {
	withRecord(t, &config.Config{Name: "web", ContPid: deadPid(t), ContPidStart: 1})
	cmd, done := startSleep(t)
	procs.Add("db", cmd.Process.Pid)

	KillStrays("web")
	assertAlive(t, done)
}

// The latest record says this registered instance is the running one: it is
// the legitimate instance, not a stray, and its cgroup is left alone too.
func TestKillStraysKeepsLatestInstance(t *testing.T) {
	legit, legitDone := startSleep(t)
	worker, workerDone := startSleep(t)
	base := withRecord(t, &config.Config{Name: "web", ContPid: legit.Process.Pid, ContPidStart: startTime(t, legit.Process.Pid)})
	procs.Add("web", legit.Process.Pid)
	writeCgroupProcs(t, base, "web", legit.Process.Pid, worker.Process.Pid)

	KillStrays("web")
	assertAlive(t, legitDone)
	assertAlive(t, workerDone)
}

// Processes in the container's cgroup are strays when the record's instance
// is dead, even if this daemon never started them (e.g. after a restart).
func TestKillStraysKillsCgroupMembers(t *testing.T) {
	base := withRecord(t, &config.Config{Name: "web", ContPid: deadPid(t), ContPidStart: 1})
	cmd, done := startSleep(t)
	writeCgroupProcs(t, base, "web", cmd.Process.Pid)

	KillStrays("web")
	assertExited(t, done)
}

// A stray that ignores SIGTERM (a pid-ns init without a handler does) is
// SIGKILLed once the TERM wait runs out.
func TestKillStraysEscalatesToKill(t *testing.T) {
	withRecord(t, &config.Config{Name: "web", ContPid: deadPid(t), ContPidStart: 1})
	prev := strayTermWait
	strayTermWait = 300 * time.Millisecond
	t.Cleanup(func() { strayTermWait = prev })
	cmd := exec.Command("sh", "-c", `trap "" TERM; exec sleep 300`)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	t.Cleanup(func() { cmd.Process.Kill(); <-done })
	time.Sleep(200 * time.Millisecond) // let sh install the ignore and exec
	procs.Add("web", cmd.Process.Pid)

	KillStrays("web")
	assertExited(t, done)
	if st := cmd.ProcessState; st == nil || st.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("want exit by SIGKILL, got %v", st)
	}
}

func writeCgroupProcs(t *testing.T, base, name string, pids ...int) {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var data []byte
	for _, p := range pids {
		data = strconv.AppendInt(data, int64(p), 10)
		data = append(data, '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "cgroup.procs"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r *procRegistry) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m = map[string][]procEntry{}
}

// An old instance's wait returning after its pid was reused and registered
// again under the same name must drop only its own entry, not the newer one.
func TestRemoveMatchesPidAndStart(t *testing.T) {
	t.Cleanup(procs.reset)
	procs.add("web", 4242, 100) // old instance
	procs.add("web", 4242, 200) // pid reused by the new instance
	procs.Remove("web", 4242, 100)

	got := procs.entries("web")
	if len(got) != 1 || got[0] != (procEntry{pid: 4242, start: 200}) {
		t.Fatalf("entries after Remove = %+v, want only the newer instance", got)
	}
}
