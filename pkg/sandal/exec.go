//go:build linux || darwin

package sandal

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/vm/mgmt"
)

// Exec dispatches exec to the native path or VM management socket. It
// returns nil when the command exits 0 and an *ExitError when it exits
// non-zero or a signal ends it; any other error is sandal's own.
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

// execReplyTimeout bounds the wait for the guest's answer to an exec
// request. The guest answers before it starts the command, so only a
// guest that is stuck takes this long.
var execReplyTimeout = 30 * time.Second

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
func execOverConn(conn net.Conn, args []string, user, dir string, tty bool, extraEnv []string,
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

	fmt.Fprintf(conn, "POST /exec HTTP/1.1\r\nHost: localhost\r\nContent-Length: %d\r\nContent-Type: application/json\r\nConnection: Upgrade\r\nUpgrade: %s, raw-stream\r\n\r\n%s", len(reqBody), mgmt.ExecUpgrade, reqBody)

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
		return fmt.Errorf("exec failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Relay I/O. Don't wait for stdin goroutine — return when output finishes.
	go io.Copy(conn, stdin)
	return relayExecOutput(resp, br, stdout)
}

// relayExecOutput copies the guest's output to stdout until the exec ends
// and returns its result as a native exec does: nil, an *ExitError, or the
// guest's error. A guest that answers raw-stream predates the exit status:
// its output is copied until EOF and the exec counts as a success.
func relayExecOutput(resp *http.Response, br *bufio.Reader, stdout io.Writer) error {
	if !strings.EqualFold(resp.Header.Get("Upgrade"), mgmt.ExecUpgrade) {
		io.Copy(stdout, br)
		return nil
	}
	x, err := mgmt.ReadExec(br, stdout)
	if err != nil {
		return fmt.Errorf("exec stream: %w", err)
	}
	if x.Error != "" {
		return errors.New(x.Error)
	}
	return crt.ExitResult(x.Code, syscall.Signal(x.Signal))
}
