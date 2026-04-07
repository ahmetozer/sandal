//go:build !linux && !darwin

package controller

import (
	"fmt"
	"net"
)

func secureSocketListen(path string) (net.Listener, error) {
	return nil, fmt.Errorf("socket listener not supported on this platform")
}
