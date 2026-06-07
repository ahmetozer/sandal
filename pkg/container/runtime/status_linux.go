//go:build linux

package runtime

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// isPidAlive checks if a process is truly alive (not a zombie) on Linux.
func isPidAlive(pid int) (bool, error) {
	data, rerr := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if rerr == nil {
		for _, line := range strings.SplitN(string(data), "\n", 5) {
			if strings.HasPrefix(line, "State:") && strings.Contains(line, "zombie") {
				return false, nil
			}
		}
	}
	return true, nil
}

// processStartTime returns the kernel start-time (field 22 of
// /proc/<pid>/stat, in clock ticks since boot) for pid. Combined with the pid
// it forms a stable identity that survives a daemon restart and defeats PID
// reuse: a recycled pid will have a different start-time.
func processStartTime(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	return parseStartTime(string(data))
}

// parseStartTime extracts field 22 (starttime) from /proc/<pid>/stat content.
// Field 2 (comm) is wrapped in parentheses and may itself contain spaces and
// parentheses, so we scan for the LAST ')' and parse the space-separated fields
// after it: the first such field is field 3 (state), so starttime (field 22)
// is at index 22-3 = 19.
func parseStartTime(stat string) (uint64, error) {
	rparen := strings.LastIndexByte(stat, ')')
	if rparen < 0 || rparen+2 >= len(stat) {
		return 0, fmt.Errorf("malformed /proc stat: no comm field")
	}
	fields := strings.Fields(stat[rparen+2:])
	const startTimeIndex = 22 - 3
	if len(fields) <= startTimeIndex {
		return 0, fmt.Errorf("malformed /proc stat: too few fields (%d)", len(fields))
	}
	st, err := strconv.ParseUint(fields[startTimeIndex], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing starttime: %w", err)
	}
	return st, nil
}
