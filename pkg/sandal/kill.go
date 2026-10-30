//go:build linux || darwin

package sandal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/host"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/vm/mgmt"
)

// Kill sends a signal to a container. For VM containers, the signal is
// routed through the management socket to the embedded controller.
func Kill(c *config.Config, signal int, timeout int) error {
	if c.VM != "" {
		return killVM(c, signal, timeout)
	}
	return host.Kill(c, signal, timeout)
}

// Seams for tests.
var (
	killViaMgmtFn  = killViaMgmt
	sendSig        = crt.SendSig
	pidRunningAs   = crt.IsPidRunningAs
	reapVMState    = reapVM
	vmPollInterval = 200 * time.Millisecond
)

// killVM sends a kill signal to a VM container. SIGKILL (9) kills the
// VM host process directly since it can't be caught anyway. Other signals
// are routed through the management socket to the container inside the VM.
// timeout follows the host.Kill contract: 0 returns without waiting, > 0
// waits up to that many seconds, < 0 waits indefinitely.
func killVM(c *config.Config, signal int, timeout int) error {
	if signal == 9 {
		return killVMHost(c, timeout)
	}
	if err := killViaMgmtFn(c.Name, signal); err != nil {
		// Management channel failed — kill the VM host process directly.
		return killVMHost(c, timeout)
	}
	// The guest powers off when its container exits (cmd/exit_linux.go), so
	// the VMM exiting is the "container stopped" event.
	return waitVMHostExit(c, timeout)
}

// killVMHost kills the VM host process (HostPid). host.Kill() can't be
// used because it targets ContPid, which is inside the VM and invisible
// from the host.
func killVMHost(c *config.Config, timeout int) error {
	pid := c.HostPid
	if pid == 0 {
		c.Status = "killed"
		return controller.SetContainer(c)
	}

	// Identity guard: a dead or recycled HostPid must not be SIGKILLed.
	if alive, _ := pidRunningAs(pid, c.HostPidStart); !alive {
		return reapVMState(c)
	}

	sendSig(pid, 9)
	return waitVMHostExit(c, timeout)
}

// waitVMHostExit waits for the VM host process to exit and then cleans up
// its state. timeout == 0 returns at once without cleanup, since the VMM
// may still be alive.
func waitVMHostExit(c *config.Config, timeout int) error {
	if timeout == 0 {
		return nil
	}
	pid := c.HostPid
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	for {
		// Identity-aware so a recycled pid counts as exited.
		if alive, _ := pidRunningAs(pid, c.HostPidStart); !alive {
			return reapVMState(c)
		}
		if timeout > 0 && !time.Now().Before(deadline) {
			return fmt.Errorf("unable to kill VM host pid %d in %d seconds", pid, timeout)
		}
		time.Sleep(vmPollInterval)
	}
}

// reapVM records an exited VM as killed and releases its host resources.
func reapVM(c *config.Config) error {
	c.Status = "killed"
	c.HostPid = 0
	c.HostPidStart = 0
	c.ContPid = 0
	c.ContPidStart = 0
	host.CleanupResources(c)
	if c.Remove {
		RemoveVMState(c)
	} else {
		controller.SetContainer(c)
	}
	return nil
}

// killViaMgmt sends a kill request to the embedded controller inside the VM.
func killViaMgmt(contName string, signal int) error {
	conn, err := mgmt.DialRaw(contName)
	if err != nil {
		return fmt.Errorf("management socket for %q: %w", contName, err)
	}
	defer conn.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"signal": signal,
	})

	fmt.Fprintf(conn, "POST /kill HTTP/1.1\r\nHost: localhost\r\nContent-Length: %d\r\nContent-Type: application/json\r\n\r\n%s", len(reqBody), reqBody)

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return fmt.Errorf("kill request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kill failed (status %d): %s", resp.StatusCode, string(body))
	}
	return nil
}
