//go:build linux && !amd64 && !arm64

package kvm

import (
	"fmt"
	"io"
	"runtime"

	"github.com/ahmetozer/sandal/pkg/container/forward"
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
)

func Boot(name string, cfg vmconfig.VMConfig, stdin io.Reader, stdout io.Writer) error {
	return unsupportedArchError()
}

func BootWithForwards(name string, cfg vmconfig.VMConfig, stdin io.Reader, stdout io.Writer, forwards []forward.PortMapping) error {
	return unsupportedArchError()
}

func unsupportedArchError() error {
	return fmt.Errorf("KVM is not supported on linux/%s; supported architectures are amd64 and arm64", runtime.GOARCH)
}
