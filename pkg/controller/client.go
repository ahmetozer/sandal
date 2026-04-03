package controller

import (
	"context"
	"net"
	"net/http"

	"github.com/ahmetozer/sandal/pkg/env"
)

type ConrollerType uint8

const (
	ControllerTypeDisk ConrollerType = iota + 1
	ControllerTypeMemory
	ControllerTypeServer
)

var (
	currentConrollerType ConrollerType = 0
	httpc                http.Client
)

func init() {
	httpc = http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", env.DaemonSocket)
			},
		},
	}
}

func GetControllerType() ConrollerType {
	return currentConrollerType
}
