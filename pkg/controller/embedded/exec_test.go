//go:build linux

package embedded

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/namespace"
)

// stubExec replaces the container lookup and the exec itself, and returns
// a pointer to the join set the handler passed on (nil until called).
func stubExec(t *testing.T) *atomic.Pointer[namespace.Namespaces] {
	t.Helper()
	var got atomic.Pointer[namespace.Namespaces]
	oldCont, oldExec := execContainer, execInContainer
	t.Cleanup(func() { execContainer, execInContainer = oldCont, oldExec })
	execContainer = func() (*config.Config, error) {
		return &config.Config{Name: "v", ContPid: os.Getpid()}, nil
	}
	execInContainer = func(c *config.Config, join namespace.Namespaces, args []string, userArg, dir string, tty bool,
		extraEnv []string, stdin io.Reader, stdout, stderr io.Writer) error {
		got.Store(&join)
		fmt.Fprint(stdout, "ran "+strings.Join(args, " "))
		return nil
	}
	return &got
}

// postExec sends body as POST /exec on a raw connection, like
// pkg/sandal's execViaMgmt, and returns the response and everything after it.
func postExec(t *testing.T, body string) (*http.Response, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(execHandler))
	t.Cleanup(srv.Close)
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST /exec HTTP/1.1\r\nHost: localhost\r\nContent-Length: %d\r\nContent-Type: application/json\r\nConnection: Upgrade\r\n\r\n%s", len(body), body)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var rest []byte
	if resp.StatusCode == http.StatusSwitchingProtocols {
		rest, _ = io.ReadAll(br)
	} else {
		rest, _ = io.ReadAll(resp.Body)
	}
	return resp, string(rest)
}

func TestExecHandlerRejectsBadNamespaceBeforeUpgrade(t *testing.T) {
	cases := []struct {
		name, body, wantInBody string
	}{
		{"bad syntax", `{"args":["true"],"ns":{"net":"abc"}}`, `-ns-net "abc"`},
		{"mnt file", `{"args":["true"],"ns":{"mnt":"file:/x"}}`, "-ns-mnt"},
		{"unknown kind", `{"args":["true"],"ns":{"foo":"host"}}`, `unknown namespace kind "foo"`},
		{"user target", `{"args":["true"],"ns":{"user":"pid:1"}}`, "-ns-user"},
		{"unknown field", `{"args":["true"],"nss":{"net":"host"}}`, "unknown field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stubExec(t)
			resp, body := postExec(t, tc.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %q)", resp.StatusCode, body)
			}
			if !strings.Contains(body, tc.wantInBody) {
				t.Fatalf("body %q does not contain %q", body, tc.wantInBody)
			}
			if got.Load() != nil {
				t.Fatal("exec ran for a rejected request")
			}
		})
	}
}

func TestExecHandlerPassesNamespaces(t *testing.T) {
	got := stubExec(t)
	resp, rest := postExec(t, `{"args":["ip","addr"],"user":"","dir":"","tty":false,"env":null,"ns":{"net":"host","uts":"pid:1"}}`)
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	if rest != "ran ip addr" {
		t.Fatalf("stream = %q, want %q", rest, "ran ip addr")
	}
	p := got.Load()
	if p == nil {
		t.Fatal("exec did not run")
	}
	join := *p
	own := fmt.Sprintf("pid:%d", os.Getpid())
	if !join["net"].IsHost {
		t.Errorf("net = %q, want host", join["net"].String())
	}
	if v := join["uts"].String(); v != "pid:1" {
		t.Errorf("uts = %q, want pid:1", v)
	}
	for _, kind := range []namespace.Name{"mnt", "pid", "ipc", "cgroup"} {
		if v := join[kind].String(); v != own {
			t.Errorf("%s = %q, want %q", kind, v, own)
		}
	}
	if _, ok := join["user"]; ok {
		t.Error("user namespace in the join set")
	}
}

func TestExecHandlerWithoutNamespaces(t *testing.T) {
	got := stubExec(t)
	resp, _ := postExec(t, `{"args":["true"],"user":"","dir":"","tty":false,"env":null}`)
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	p := got.Load()
	if p == nil {
		t.Fatal("exec did not run")
	}
	own := fmt.Sprintf("pid:%d", os.Getpid())
	for _, kind := range namespace.ExecKinds {
		if v := (*p)[kind].String(); v != own {
			t.Errorf("%s = %q, want %q", kind, v, own)
		}
	}
}
