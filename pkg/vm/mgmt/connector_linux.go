//go:build linux

package mgmt

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// VsockConnector connects to a KVM guest via AF_VSOCK.
type VsockConnector struct {
	GuestCID uint32
	Port     uint32
}

// Connect dials the guest via AF_VSOCK and returns a file-based connection.
func (c VsockConnector) Connect() (io.ReadWriteCloser, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("vsock socket: %w", err)
	}
	err = unix.Connect(fd, &unix.SockaddrVM{CID: c.GuestCID, Port: c.Port})
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("vsock connect CID=%d port=%d: %w", c.GuestCID, c.Port, err)
	}
	return os.NewFile(uintptr(fd), fmt.Sprintf("vsock:%d:%d", c.GuestCID, c.Port)), nil
}
