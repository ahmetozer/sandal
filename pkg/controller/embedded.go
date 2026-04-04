//go:build linux

package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/console"
	containerexec "github.com/ahmetozer/sandal/pkg/container/exec"
	"github.com/ahmetozer/sandal/pkg/container/snapshot"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/squashfs"
	"github.com/ahmetozer/sandal/pkg/vm/guest"
)

// StartEmbeddedController starts a lightweight HTTP API server inside the VM
// for management commands from the macOS host (via vsock relay).
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
		c, err := Containers()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(c)
	})

	mux.HandleFunc("/containers/{name}", func(w http.ResponseWriter, r *http.Request) {
		c, err := GetContainer(r.PathValue("name"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(c)
	})

	mux.HandleFunc("POST /exec", embeddedExecHandler)
	mux.HandleFunc("POST /attach", embeddedAttachHandler)
	mux.HandleFunc("POST /snapshot", embeddedSnapshotHandler)
	mux.HandleFunc("POST /export", embeddedExportHandler)

	slog.Info("embedded controller ready", slog.String("socket", sockPath))
	http.Serve(ln, mux)
}

// execRequest is the JSON body for /exec.
type execRequest struct {
	Container string   `json:"container"`
	Args      []string `json:"args"`
	User      string   `json:"user"`
	Dir       string   `json:"dir"`
}

// embeddedExecHandler runs a command inside the container's namespaces.
// It uses HTTP 101 upgrade to relay stdin/stdout/stderr as a raw stream.
func embeddedExecHandler(w http.ResponseWriter, r *http.Request) {
	var req execRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	c, err := getFirstContainer()
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

	// Hijack the connection for raw I/O
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

	// Send 101 upgrade
	bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	bufrw.WriteString("Connection: Upgrade\r\n")
	bufrw.WriteString("Upgrade: raw-stream\r\n\r\n")
	bufrw.Flush()

	// Call the same exec function used by the native Linux CLI.
	// ExecInContainer uses LockOSThread + SetNS to enter the container's
	// namespaces on a dedicated OS thread — safe for concurrent handlers.
	if err := containerexec.ExecInContainer(c, req.Args, req.User, req.Dir, conn, conn, conn); err != nil {
		fmt.Fprintf(conn, "\n[exec error: %v]\n", err)
	}
}

// attachRequest is the JSON body for /attach.
type attachRequest struct {
	Container string `json:"container"`
}

// embeddedAttachHandler connects to the container's console socket and relays.
func embeddedAttachHandler(w http.ResponseWriter, r *http.Request) {
	var req attachRequest
	json.NewDecoder(r.Body).Decode(&req)

	c, err := getFirstContainer()
	if err != nil {
		http.Error(w, "container not found: "+err.Error(), http.StatusNotFound)
		return
	}

	// Find console socket
	sockPath := console.SocketPath(c.Name)
	if _, err := os.Stat(sockPath); err != nil {
		http.Error(w, "no console socket available", http.StatusNotFound)
		return
	}

	// Hijack for raw I/O
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

	// Connect to the console socket
	consoleConn, err := net.Dial("unix", sockPath)
	if err != nil {
		return
	}
	defer consoleConn.Close()

	// Bidirectional relay
	done := make(chan struct{})
	go func() {
		io.Copy(consoleConn, conn)
		done <- struct{}{}
	}()
	io.Copy(conn, consoleConn)
	<-done
}

// snapshotRequest is the JSON body for /snapshot.
type snapshotRequest struct {
	Container string   `json:"container"`
	FilePath  string   `json:"filePath"`
	Includes  []string `json:"includes"`
	Excludes  []string `json:"excludes"`
}

// embeddedSnapshotHandler creates a snapshot of the container's changes.
func embeddedSnapshotHandler(w http.ResponseWriter, r *http.Request) {
	var req snapshotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	c, err := getFirstContainer()
	if err != nil {
		http.Error(w, "container not found: "+err.Error(), http.StatusNotFound)
		return
	}

	outPath, err := snapshot.Create(c, req.FilePath, snapshot.FilterOptions{
		Includes: req.Includes,
		Excludes: req.Excludes,
	})
	if err != nil {
		http.Error(w, "snapshot failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"path": outPath})
}

// exportRequest is the JSON body for /export.
type exportRequest struct {
	Container  string   `json:"container"`
	OutputPath string   `json:"outputPath"`
	Includes   []string `json:"includes"`
	Excludes   []string `json:"excludes"`
}

// embeddedExportHandler exports the container's full filesystem as squashfs.
func embeddedExportHandler(w http.ResponseWriter, r *http.Request) {
	var req exportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	c, err := getFirstContainer()
	if err != nil {
		http.Error(w, "container not found: "+err.Error(), http.StatusNotFound)
		return
	}

	rootfsDir := c.RootfsDir
	if _, err := os.Stat(rootfsDir); err != nil {
		http.Error(w, "rootfs not found (is the container running?)", http.StatusNotFound)
		return
	}

	outputPath := req.OutputPath
	if outputPath == "" {
		outputPath = filepath.Join(env.BaseSnapshotDir, c.Name+"-export.sqfs")
	}

	outFile, err := os.Create(outputPath)
	if err != nil {
		http.Error(w, "create output: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer outFile.Close()

	var opts []squashfs.WriterOption
	if len(req.Includes) > 0 || len(req.Excludes) > 0 {
		inc := req.Includes
		if len(inc) == 0 {
			inc = []string{"/"}
		}
		opts = append(opts, squashfs.WithPathFilter(
			squashfs.NewIncludeExcludeFilter(inc, req.Excludes),
		))
	}

	sqWriter, err := squashfs.NewWriter(outFile, opts...)
	if err != nil {
		os.Remove(outputPath)
		http.Error(w, "squashfs writer: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := sqWriter.CreateFromDir(rootfsDir); err != nil {
		os.Remove(outputPath)
		http.Error(w, "export failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"path": outputPath})
}

// getFirstContainer returns the first (and typically only) container running in this VM.
func getFirstContainer() (*config.Config, error) {
	conts, err := Containers()
	if err != nil {
		return nil, err
	}
	if len(conts) == 0 {
		return nil, fmt.Errorf("no containers found")
	}
	return conts[0], nil
}

