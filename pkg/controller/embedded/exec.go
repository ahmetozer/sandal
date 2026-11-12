//go:build linux

package embedded

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	containerexec "github.com/ahmetozer/sandal/pkg/container/exec"
)

type execRequest struct {
	Container string   `json:"container"`
	Args      []string `json:"args"`
	User      string   `json:"user"`
	Dir       string   `json:"dir"`
	TTY       bool     `json:"tty"`
	Env       []string `json:"env"`
	// NS holds the -ns-<kind> values by kind ("net" -> "cont:db"). Targets
	// refer to this VM's kernel: host is the VM's own namespace, pids and
	// files are guest ones, and cont: only finds this VM's container.
	NS map[string]string `json:"ns"`
}

// Indirections for tests: the container lookup reads controller state and
// the exec itself joins namespaces.
var (
	execContainer   = getFirstContainer
	execInContainer = containerexec.ExecInContainer
)

// execHandler runs a command inside the container's namespaces.
// It uses HTTP 101 upgrade to relay stdin/stdout/stderr as a raw stream.
// The namespace set is built and checked before the upgrade, so a bad
// -ns-<kind> target is answered with 400 instead of text in the stream.
func execHandler(w http.ResponseWriter, r *http.Request) {
	var req execRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	c, err := execContainer()
	if err != nil {
		http.Error(w, "container not found: "+err.Error(), http.StatusNotFound)
		return
	}
	if req.Container != "" && req.Container != c.Name {
		http.Error(w, "container not found", http.StatusNotFound)
		return
	}

	if len(req.Args) == 0 {
		http.Error(w, "args required", http.StatusBadRequest)
		return
	}

	join, err := containerexec.JoinNamespaces(c, req.NS)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	conn, bufrw, err := hj.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	bufrw.WriteString("Connection: Upgrade\r\n")
	bufrw.WriteString("Upgrade: raw-stream\r\n\r\n")
	bufrw.Flush()

	if err := execInContainer(c, join, req.Args, req.User, req.Dir, req.TTY, req.Env, conn, conn, conn); err != nil {
		errStr := err.Error()
		if !strings.Contains(errStr, "broken pipe") && !strings.Contains(errStr, "exit status") {
			fmt.Fprintf(conn, "\n[exec error: %v]\n", err)
		}
	}
}
