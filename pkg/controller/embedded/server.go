//go:build linux

package embedded

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/vm/guest"
)

// StartEmbeddedController starts a lightweight HTTP API server inside the VM
// for management commands from the host (via vsock relay).
// Must be called as a goroutine — blocks forever.
func StartEmbeddedController() {
	sockPath := guest.ControllerSocketPath
	os.MkdirAll(filepath.Dir(sockPath), 0o755)
	os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		slog.Warn("embedded controller: listen", slog.Any("err", err))
		return
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/containers", func(w http.ResponseWriter, r *http.Request) {
		c, err := controller.Containers()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(c)
	})

	mux.HandleFunc("/containers/{name}", func(w http.ResponseWriter, r *http.Request) {
		c, err := controller.GetContainer(r.PathValue("name"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(c)
	})

	mux.HandleFunc("POST /exec", execHandler)
	mux.HandleFunc("POST /kill", killHandler)
	mux.HandleFunc("POST /attach", attachHandler)
	mux.HandleFunc("POST /snapshot", snapshotHandler)
	mux.HandleFunc("POST /export", exportHandler)

	slog.Info("embedded controller ready", slog.String("socket", sockPath))
	http.Serve(ln, mux)
}

// getFirstContainer returns the first (and typically only) container running in this VM.
func getFirstContainer() (*config.Config, error) {
	conts, err := controller.Containers()
	if err != nil {
		return nil, err
	}
	if len(conts) == 0 {
		return nil, fmt.Errorf("no containers found")
	}
	return conts[0], nil
}
