//go:build linux

package host

import (
	"bufio"
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

// procEntry identifies one container process by pid plus kernel start-time,
// so a pid that is later reused by an unrelated process never matches.
type procEntry struct {
	pid   int
	start uint64
}

// procRegistry remembers every container process this process started, by
// name, independent of the state record. If the record loses an instance's
// pid (a lost state update), recovery can still find and stop it instead of
// starting a duplicate that fails to bind the old one's ports.
type procRegistry struct {
	mu sync.Mutex
	m  map[string][]procEntry
}

var procs = &procRegistry{m: map[string][]procEntry{}}

// Add records pid under name with its current start-time. A pid whose
// start-time can't be read is not recorded: without an identity a later
// reuse of that pid could not be told apart, so it must never be killed.
// Returns the recorded start-time (0 when not recorded) for Remove.
func (r *procRegistry) Add(name string, pid int) uint64 {
	st, err := crt.ProcessStartTime(pid)
	if err != nil || st == 0 {
		slog.Warn("procreg: capture start-time", "cont", name, "pid", pid, "error", err)
		return 0
	}
	r.add(name, pid, st)
	return st
}

func (r *procRegistry) add(name string, pid int, start uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[name] = append(r.m[name], procEntry{pid: pid, start: start})
}

// Remove drops the entry with this pid and start-time from name. Only that
// one: a newer instance of the same name may already be registered when the
// old one's wait returns, possibly under the same (reused) pid.
func (r *procRegistry) Remove(name string, pid int, start uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.m[name][:0]
	for _, e := range r.m[name] {
		if e.pid != pid || e.start != start {
			kept = append(kept, e)
		}
	}
	if len(kept) == 0 {
		delete(r.m, name)
	} else {
		r.m[name] = kept
	}
}

func (r *procRegistry) entries(name string) []procEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]procEntry(nil), r.m[name]...)
}

// strayCgroupBase is where crun places containers with resource limits
// (resources.SetupCgroup). A var so tests can point it at a temp dir.
var strayCgroupBase = "/sys/fs/cgroup/sandal"

// Vars so tests can shorten them.
var (
	strayTermWait = 10 * time.Second // mirrors contRecover's Kill(cont, 15, 10)
	strayKillWait = 2 * time.Second  // let the SIGKILLed process exit and free its ports
)

// KillStrays stops every previous instance of name that the state record no
// longer points at: processes this daemon started under name (procs) and
// members of the container's cgroup. SIGTERM, wait up to 10 s, then SIGKILL.
//
// The instance the latest record says is running is never touched: its
// identity is excluded, and while it is alive the cgroup is skipped entirely
// because its members are that instance's own children. Callers hold the
// per-name lifecycle lock, and every placer publishes the new pid before
// releasing that lock, so the record read here already names any legitimate
// instance.
//
// Returns how many strays it signalled, so the caller can redo the cleanup
// that failed while they were still alive (busy mounts, cgroup).
func KillStrays(name string) int {
	latest, err := controller.GetContainer(name)
	if err != nil {
		slog.Warn("recover: stray check skipped", "cont", name, "error", err)
		return 0
	}
	keepPid, keepStart := latest.MonitorPidIdentity()
	keepAlive, _ := crt.IsPidRunningAs(keepPid, keepStart)
	isKeep := func(e procEntry) bool {
		return keepAlive && e.pid == keepPid && (keepStart == 0 || e.start == keepStart)
	}

	var targets []procEntry
	seen := map[int]bool{os.Getpid(): true}
	consider := func(e procEntry) {
		if seen[e.pid] || isKeep(e) || !sameProcess(e) {
			return
		}
		seen[e.pid] = true
		targets = append(targets, e)
	}
	for _, e := range procs.entries(name) {
		consider(e)
	}
	cgDir := filepath.Join(strayCgroupBase, name)
	if !keepAlive {
		for _, pid := range cgroupProcs(cgDir) {
			// No start-time was recorded for cgroup members; take it now so
			// a member that exits and whose pid is reused during the wait
			// below is not SIGKILLed.
			if st, err := crt.ProcessStartTime(pid); err == nil && st != 0 {
				consider(procEntry{pid: pid, start: st})
			}
		}
	}
	if len(targets) == 0 {
		return 0
	}

	for _, e := range targets {
		slog.Warn("recover: killing untracked previous instance", "cont", name, "pid", e.pid)
		crt.SendSig(e.pid, 15)
	}
	if waitStrays(targets, strayTermWait) {
		return len(targets)
	}
	if !keepAlive {
		// cgroup v2: kills every member, including any forked after listing.
		if err := os.WriteFile(filepath.Join(cgDir, "cgroup.kill"), []byte("1"), 0o644); err != nil && !os.IsNotExist(err) {
			slog.Debug("recover: cgroup.kill", "cont", name, "error", err)
		}
	}
	for _, e := range targets {
		if sameProcess(e) {
			crt.SendSig(e.pid, 9)
		}
	}
	if !waitStrays(targets, strayKillWait) {
		slog.Warn("recover: previous instance still alive after SIGKILL", "cont", name)
	}
	return len(targets)
}

// sameProcess reports whether e's pid is alive and still has e's start-time.
// Stricter than crt.IsPidRunningAs, which reports alive when the start-time
// can't be read: here that would mean signalling an unverified pid.
func sameProcess(e procEntry) bool {
	if alive, _ := crt.IsPidRunning(e.pid); !alive {
		return false
	}
	st, err := crt.ProcessStartTime(e.pid)
	return err == nil && st == e.start
}

// waitStrays polls until every target has exited (or its pid was reused) or
// the timeout passes. Reports whether all are gone.
func waitStrays(targets []procEntry, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		alive := false
		for _, e := range targets {
			if sameProcess(e) {
				alive = true
				break
			}
		}
		if !alive {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// cgroupProcs lists the pids in dir/cgroup.procs; nil when the container has
// no cgroup (no limits set, or no cgroup v2 on this host).
func cgroupProcs(dir string) []int {
	data, err := os.ReadFile(filepath.Join(dir, "cgroup.procs"))
	if err != nil {
		return nil
	}
	var pids []int
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if pid, err := strconv.Atoi(sc.Text()); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}
