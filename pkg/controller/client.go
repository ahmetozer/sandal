package controller

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/ahmetozer/sandal/pkg/env"
)

type ConrollerType uint8

const (
	ControllerTypeDisk ConrollerType = iota + 1
	ControllerTypeMemory
	ControllerTypeServer
)

// daemonTimeout bounds every call to the daemon socket, so a stuck daemon
// cannot hang the caller. Callers fall back to the state files on a timeout.
const daemonTimeout = 5 * time.Second

var (
	currentConrollerType ConrollerType = 0
	httpc                http.Client
)

func init() {
	httpc = http.Client{
		Timeout: daemonTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", env.DaemonSocket)
			},
		},
	}
}

func GetControllerType() ConrollerType {
	return currentConrollerType
}
