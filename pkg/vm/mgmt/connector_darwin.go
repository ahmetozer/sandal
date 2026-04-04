//go:build darwin

package mgmt

import (
	"io"
	"os"
)

// VZConnector connects to a VZ guest via the Virtualization.framework vsock.
type VZConnector struct {
	VM   interface{ ConnectVsock(uint32) (*os.File, error) }
	Port uint32
}

// Connect calls the VZ VM's ConnectVsock and returns the file as a ReadWriteCloser.
func (c VZConnector) Connect() (io.ReadWriteCloser, error) {
	return c.VM.ConnectVsock(c.Port)
}
