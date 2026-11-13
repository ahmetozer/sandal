//go:build linux || darwin

package sandal

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall"

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
func Exec(c *config.Config, args []string, user, dir string, tty bool, extraEnv []string,
	stdin io.Reader, stdout, stderr io.Writer) error {

	if c.VM != "" {
		return execViaMgmt(c.Name, args, user, dir, tty, extraEnv, stdin, stdout)
	}

	return execNative(c, args, user, dir, tty, extraEnv)
}

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

	fmt.Fprintf(conn, "POST /exec HTTP/1.1\r\nHost: localhost\r\nContent-Length: %d\r\nContent-Type: application/json\r\nConnection: Upgrade\r\nUpgrade: %s, raw-stream\r\n\r\n%s", len(reqBody), mgmt.ExecUpgrade, reqBody)

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
