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
	"github.com/ahmetozer/sandal/pkg/vm/mgmt"
)

// Exec dispatches exec to the native path or VM management socket.
// The caller is responsible for terminal setup (raw mode), signal handling,
// and container lookup. stdin/stdout/stderr allow reuse from CLI, web, etc.
// extraEnv contains extra environment variables (KEY=VALUE) to pass to the
// command inside the container (e.g. from -env-pass / -env-all).
func Exec(c *config.Config, args []string, user, dir string, tty bool, extraEnv []string,
	stdin io.Reader, stdout, stderr io.Writer) error {

	if c.VM != "" {
		return execViaMgmt(c.Name, args, user, dir, tty, extraEnv, stdin, stdout)
	}

	return execNative(c, args, user, dir, tty, extraEnv)
}

// execReplyTimeout bounds the wait for the guest's answer to an exec
// request. The guest answers before it starts the command, so only a
// guest that is stuck takes this long.
var execReplyTimeout = 30 * time.Second

// execViaMgmt sends an exec request to the VM's embedded controller
// via the management socket and relays I/O.
func execViaMgmt(contName string, args []string, user, dir string, tty bool, extraEnv []string,
	stdin io.Reader, stdout io.Writer) error {

	conn, err := mgmt.DialRaw(contName)
	if err != nil {
		return fmt.Errorf("management socket for %q: %w", contName, err)
	}
	defer conn.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"args": args,
		"user": user,
		"dir":  dir,
		"tty":  tty,
		"env":  extraEnv,
	})

	fmt.Fprintf(conn, "POST /exec HTTP/1.1\r\nHost: localhost\r\nContent-Length: %d\r\nContent-Type: application/json\r\nConnection: Upgrade\r\n\r\n%s", len(reqBody), reqBody)

	// The host side closes the connection when it cannot reach the guest's
	// controller (e.g. the VM is still booting); a stuck guest never answers.
	br := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(execReplyTimeout))
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return fmt.Errorf("exec request failed: the VM guest did not answer: %w", err)
	}
	conn.SetReadDeadline(time.Time{})
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("exec failed (status %d): %s", resp.StatusCode, string(body))
	}

	// Relay I/O. Don't wait for stdin goroutine — return when output finishes.
	go io.Copy(conn, stdin)
	io.Copy(stdout, br)

	return nil
}
