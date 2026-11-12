//go:build linux || darwin

package sandal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// fakeGuest reads one exec request from conn, hands its JSON body to
// inspect, and answers with reply.
func fakeGuest(t *testing.T, conn net.Conn, reply string, inspect func(map[string]any)) {
	t.Helper()
	defer conn.Close()
	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		t.Errorf("read request: %v", err)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Errorf("decode body: %v", err)
		return
	}
	inspect(body)
	io.WriteString(conn, reply)
}

func TestExecOverConnSendsNamespaces(t *testing.T) {
	client, guest := net.Pipe()
	defer client.Close()
	var body map[string]any
	done := make(chan struct{})
	go func() {
		defer close(done)
		fakeGuest(t, guest, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: raw-stream\r\n\r\nhello", func(b map[string]any) { body = b })
	}()

	var out bytes.Buffer
	ns := map[string]string{"net": "cont:db", "mnt": "host"}
	if err := execOverConn(client, []string{"ss", "-ltn"}, "", "", false, nil, ns, strings.NewReader(""), &out); err != nil {
		t.Fatalf("execOverConn = %v", err)
	}
	<-done
	if out.String() != "hello" {
		t.Fatalf("output = %q, want hello", out.String())
	}
	raw, ok := body["ns"].(map[string]any)
	if !ok {
		t.Fatalf("request body has no ns object: %v", body)
	}
	got := map[string]string{}
	for k, v := range raw {
		got[k], _ = v.(string)
	}
	if !maps.Equal(got, ns) {
		t.Fatalf("ns = %v, want %v", got, ns)
	}
}

func TestExecOverConnOmitsEmptyNamespaces(t *testing.T) {
	client, guest := net.Pipe()
	defer client.Close()
	var body map[string]any
	done := make(chan struct{})
	go func() {
		defer close(done)
		fakeGuest(t, guest, "HTTP/1.1 101 Switching Protocols\r\n\r\n", func(b map[string]any) { body = b })
	}()
	if err := execOverConn(client, []string{"true"}, "", "", false, nil, nil, strings.NewReader(""), io.Discard); err != nil {
		t.Fatalf("execOverConn = %v", err)
	}
	<-done
	if _, ok := body["ns"]; ok {
		t.Fatalf("request carries ns without -ns-<kind> flags: %v", body)
	}
}

func TestExecOverConnReturnsGuestError(t *testing.T) {
	client, guest := net.Pipe()
	defer client.Close()
	msg := `-ns-net cont:nope: container "nope" not found`
	go fakeGuest(t, guest, "HTTP/1.1 400 Bad Request\r\nContent-Type: text/plain\r\nContent-Length: "+
		strconv.Itoa(len(msg)+1)+"\r\n\r\n"+msg+"\n", func(map[string]any) {})
	err := execOverConn(client, []string{"true"}, "", "", false, nil, map[string]string{"net": "cont:nope"}, strings.NewReader(""), io.Discard)
	if err == nil {
		t.Fatal("execOverConn = nil, want the guest's 400")
	}
	if want := "exec failed (status 400): " + msg; err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}
