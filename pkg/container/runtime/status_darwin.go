//go:build darwin

package runtime

import (
	"fmt"
	"os/exec"
	"strings"
)

// isPidAlive checks if a process is truly alive (not a zombie) on macOS
// by inspecting the process state via ps.
func isPidAlive(pid int) (bool, error) {
	out, err := exec.Command("ps", "-o", "state=", "-p", fmt.Sprintf("%d", pid)).Output()
	if err != nil {
		// Process not found — not alive.
		return false, nil
	}
	state := strings.TrimSpace(string(out))
	if strings.HasPrefix(state, "Z") {
		return false, nil
	}
	return true, nil
}
