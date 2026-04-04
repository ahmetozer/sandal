package mgmt

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/env"
)

// SocketDir returns the directory containing per-container management sockets.
func SocketDir() string {
	return filepath.Join(env.RunDir, "sock")
}

// SocketPath returns the management socket path for a container.
func SocketPath(name string) string {
	return filepath.Join(SocketDir(), name+".sock")
}

// NewHTTPClient creates an HTTP client that connects to the container's management socket.
func NewHTTPClient(name string) (*http.Client, error) {
	sockPath := SocketPath(name)
	if _, err := os.Stat(sockPath); err != nil {
		return nil, err
	}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}, nil
}

// DialRaw connects to the container's management socket and returns a raw connection.
func DialRaw(name string) (net.Conn, error) {
	return net.Dial("unix", SocketPath(name))
}
