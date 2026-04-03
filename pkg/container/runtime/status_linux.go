//go:build linux

package runtime

import (
	"fmt"
	"os"
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
