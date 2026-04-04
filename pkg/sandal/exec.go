//go:build linux || darwin

package sandal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/vm/mgmt"
)

// Exec dispatches exec to the native path or VM management socket.
// The caller is responsible for terminal setup (raw mode), signal handling,
// and container lookup. stdin/stdout/stderr allow reuse from CLI, web, etc.
func Exec(c *config.Config, args []string, user, dir string, tty bool,
	stdin io.Reader, stdout, stderr io.Writer) error {

	if c.VM != "" {
		return execViaMgmt(c.Name, args, user, dir, tty, stdin, stdout)
	}

	return execNative(c, args, user, dir, tty)
}

// execViaMgmt sends an exec request to the VM's embedded controller
// via the management socket and relays I/O.
func execViaMgmt(contName string, args []string, user, dir string, tty bool,
	stdin io.Reader, stdout io.Writer) error {

	conn, err := mgmt.DialRaw(contName)
	if err != nil {
		return fmt.Errorf("management socket for %q: %w", contName, err)
	}
	defer conn.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"container": contName,
		"args":      args,
		"user":      user,
		"dir":       dir,
		"tty":       tty,
	})

	fmt.Fprintf(conn, "POST /exec HTTP/1.1\r\nHost: localhost\r\nContent-Length: %d\r\nContent-Type: application/json\r\nConnection: Upgrade\r\n\r\n%s", len(reqBody), reqBody)

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return fmt.Errorf("exec request failed: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("exec failed (status %d): %s", resp.StatusCode, string(body))
	}

	// Relay I/O. Don't wait for stdin goroutine — return when output finishes.
	go io.Copy(conn, stdin)
	io.Copy(stdout, br)

	return nil
}
