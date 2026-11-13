//go:build linux

package embedded

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	containerexec "github.com/ahmetozer/sandal/pkg/container/exec"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/vm/mgmt"
)

type execRequest struct {
	Container string   `json:"container"`
	Args      []string `json:"args"`
	User      string   `json:"user"`
	Dir       string   `json:"dir"`
	TTY       bool     `json:"tty"`
	Env       []string `json:"env"`
}

// Indirections for tests: the container lookup reads controller state and
// the exec itself joins namespaces.
var (
	execContainer   = getFirstContainer
	execInContainer = containerexec.ExecInContainer
)

// execHandler runs a command inside the container's namespaces.
// It uses HTTP 101 upgrade to relay stdin/stdout/stderr as a raw stream.
// When the host offers mgmt.ExecUpgrade, the output is framed and followed
// by the command's exit status.
func execHandler(w http.ResponseWriter, r *http.Request) {
	var req execRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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

	framed := mgmt.OffersExecUpgrade(r.Header)
	upgrade := "raw-stream"
	if framed {
		upgrade = mgmt.ExecUpgrade
	}
	bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	bufrw.WriteString("Connection: Upgrade\r\n")
	bufrw.WriteString("Upgrade: " + upgrade + "\r\n\r\n")
	bufrw.Flush()

	if framed {
		out := mgmt.NewExecWriter(conn)
		err := execInContainer(c, req.Args, req.User, req.Dir, req.TTY, req.Env, conn, out, out)
		out.Exit(execExit(err))
		return
	}

	if err := execInContainer(c, req.Args, req.User, req.Dir, req.TTY, req.Env, conn, conn, conn); err != nil {
		var exitErr *crt.ExitError
		if !errors.As(err, &exitErr) && !strings.Contains(err.Error(), "broken pipe") {
			fmt.Fprintf(conn, "\n[exec error: %v]\n", err)
		}
	}
}

// execExit is the exit frame for the result of ExecInContainer.
func execExit(err error) mgmt.ExecExit {
	var exitErr *crt.ExitError
	switch {
	case err == nil:
		return mgmt.ExecExit{}
	case errors.As(err, &exitErr):
		return mgmt.ExecExit{Code: exitErr.Code, Signal: int(exitErr.Signal)}
	default:
		return mgmt.ExecExit{Code: 1, Error: err.Error()}
	}
}
