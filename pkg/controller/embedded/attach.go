//go:build linux

package embedded

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"

	"github.com/ahmetozer/sandal/pkg/container/console"
)

type attachRequest struct {
	Container string `json:"container"`
}

// attachHandler connects to the container's console socket and relays I/O.
func attachHandler(w http.ResponseWriter, r *http.Request) {
	var req attachRequest
	json.NewDecoder(r.Body).Decode(&req)

	c, err := getFirstContainer()
	if err != nil {
		http.Error(w, "container not found: "+err.Error(), http.StatusNotFound)
		return
	}

	sockPath := console.SocketPath(c.Name)
	if _, err := os.Stat(sockPath); err != nil {
		http.Error(w, "no console socket available", http.StatusNotFound)
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

	consoleConn, err := net.Dial("unix", sockPath)
	if err != nil {
		return
	}
	defer consoleConn.Close()

	done := make(chan struct{})
	go func() {
		io.Copy(consoleConn, conn)
		done <- struct{}{}
	}()
	io.Copy(conn, consoleConn)
	<-done
}
