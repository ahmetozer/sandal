//go:build linux

package embedded

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/vm/mgmt"
)

// stubExecResult replaces the container lookup and the exec itself with one
// that writes "out" and returns result.
func stubExecResult(t *testing.T, result error) {
	t.Helper()
	oldCont, oldExec := execContainer, execInContainer
	t.Cleanup(func() { execContainer, execInContainer = oldCont, oldExec })
	execContainer = func() (*config.Config, error) {
		return &config.Config{Name: "v", ContPid: os.Getpid()}, nil
	}
	execInContainer = func(c *config.Config, args []string, userArg, dir string, tty bool,
		extraEnv []string, stdin io.Reader, stdout, stderr io.Writer) error {
		fmt.Fprint(stdout, "out")
		return result
	}
}

// postExecUpgrade sends POST /exec like pkg/sandal's execViaMgmt, with the
// given Upgrade offer (none when empty), and returns the 101 response and a
// reader for the stream after it.
func postExecUpgrade(t *testing.T, upgrade string) (*http.Response, *bufio.Reader) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(execHandler))
	t.Cleanup(srv.Close)
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	body := `{"args":["sh","-c","exit 3"],"tty":true}`
	if upgrade != "" {
		upgrade = "Upgrade: " + upgrade + "\r\n"
	}
	fmt.Fprintf(conn, "POST /exec HTTP/1.1\r\nHost: localhost\r\nContent-Length: %d\r\nContent-Type: application/json\r\nConnection: Upgrade\r\n%s\r\n%s", len(body), upgrade, body)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	return resp, br
}

// A host that offers the framed upgrade gets the output and then how the
// command ended.
func TestExecHandlerSendsExitStatus(t *testing.T) {
	cases := []struct {
		name   string
		result error
		want   mgmt.ExecExit
	}{
		{"exit 0", nil, mgmt.ExecExit{}},
		{"exit 3", crt.ExitResult(3, 0), mgmt.ExecExit{Code: 3}},
		{"killed", crt.ExitResult(-1, syscall.SIGKILL), mgmt.ExecExit{Code: 137, Signal: 9}},
		{"exec error", errors.New("lookup nope in PATH"), mgmt.ExecExit{Code: 1, Error: "lookup nope in PATH"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubExecResult(t, tc.result)
			resp, br := postExecUpgrade(t, mgmt.ExecUpgrade+", raw-stream")
			if got := resp.Header.Get("Upgrade"); got != mgmt.ExecUpgrade {
				t.Fatalf("Upgrade = %q, want %q", got, mgmt.ExecUpgrade)
			}
			var out strings.Builder
			got, err := mgmt.ReadExec(br, &out)
			if err != nil {
				t.Fatalf("ReadExec = %v", err)
			}
			if out.String() != "out" {
				t.Errorf("output = %q, want out", out.String())
			}
			if got != tc.want {
				t.Errorf("exit = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// A host that predates the framed upgrade gets the plain stream, and a
// non-zero exit is not reported in it as an exec error.
func TestExecHandlerRawStreamForOldHosts(t *testing.T) {
	stubExecResult(t, crt.ExitResult(3, 0))
	resp, br := postExecUpgrade(t, "")
	if got := resp.Header.Get("Upgrade"); got != "raw-stream" {
		t.Fatalf("Upgrade = %q, want raw-stream", got)
	}
	rest, _ := io.ReadAll(br)
	if string(rest) != "out" {
		t.Fatalf("stream = %q, want out", rest)
	}
}
