//go:build linux

package embedded

import (
	"encoding/json"
	"fmt"
	"net/http"
	"syscall"
)

type killRequest struct {
	Signal int `json:"signal"`
}

// killHandler sends a signal to the container process inside the VM.
func killHandler(w http.ResponseWriter, r *http.Request) {
	var req killRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Signal == 0 {
		req.Signal = 9
	}

	c, err := getFirstContainer()
	if err != nil {
		http.Error(w, "container not found: "+err.Error(), http.StatusNotFound)
		return
	}

	if c.ContPid == 0 {
		http.Error(w, "container not running", http.StatusGone)
		return
	}

	if err := syscall.Kill(c.ContPid, syscall.Signal(req.Signal)); err != nil {
		http.Error(w, fmt.Sprintf("kill pid %d: %v", c.ContPid, err), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
