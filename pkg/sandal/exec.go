//go:build linux || darwin

package sandal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/vm/mgmt"
)

// Exec dispatches exec to the native path or VM management socket.
// The caller is responsible for terminal setup (raw mode), signal handling,
// and container lookup. stdin/stdout/stderr allow reuse from CLI, web, etc.
// extraEnv contains extra environment variables (KEY=VALUE) to pass to the
// command inside the container (e.g. from -env-pass / -env-all).
//
// ns holds the -ns-<kind> values by kind (see namespace.ExecFlags); a kind
// that is missing means the container's own namespace. For a VM container
// the map is forwarded to the guest, which validates and resolves it
// against the VM's kernel.
func Exec(c *config.Config, args []string, user, dir string, tty bool, extraEnv []string,
	ns map[string]string, stdin io.Reader, stdout, stderr io.Writer) error {

	if c.VM != "" {
		return execViaMgmt(c.Name, args, user, dir, tty, extraEnv, ns, stdin, stdout)
	}

	return execNative(c, args, user, dir, tty, extraEnv, ns)
}

// execViaMgmt sends an exec request to the VM's embedded controller
// via the management socket and relays I/O.
func execViaMgmt(contName string, args []string, user, dir string, tty bool, extraEnv []string,
	ns map[string]string, stdin io.Reader, stdout io.Writer) error {

	conn, err := mgmt.DialRaw(contName)
	if err != nil {
		return fmt.Errorf("management socket for %q: %w", contName, err)
	}
	defer conn.Close()

	return execOverConn(conn, args, user, dir, tty, extraEnv, ns, stdin, stdout)
}

// execOverConn writes the exec request to conn, waits for the 101 upgrade
// and relays I/O until the output side ends. A non-101 answer (e.g. 400 for
// a bad -ns-<kind> target) is returned as an error with the guest's message.
func execOverConn(conn io.ReadWriter, args []string, user, dir string, tty bool, extraEnv []string,
	ns map[string]string, stdin io.Reader, stdout io.Writer) error {

	req := map[string]any{
		"args": args,
		"user": user,
		"dir":  dir,
		"tty":  tty,
		"env":  extraEnv,
	}
	// Only sent when set, so a plain exec stays the request older guests
	// already understand. A guest that predates the field ignores it and
	// joins the container's own namespaces.
	if len(ns) > 0 {
		req["ns"] = ns
	}
	reqBody, _ := json.Marshal(req)

	fmt.Fprintf(conn, "POST /exec HTTP/1.1\r\nHost: localhost\r\nContent-Length: %d\r\nContent-Type: application/json\r\nConnection: Upgrade\r\n\r\n%s", len(reqBody), reqBody)

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return fmt.Errorf("exec request failed: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("exec failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Relay I/O. Don't wait for stdin goroutine — return when output finishes.
	go io.Copy(conn, stdin)
	io.Copy(stdout, br)

	return nil
}
