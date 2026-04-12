//go:build darwin

package forward

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"
)

// vsockBasePortDarwin mirrors VsockBasePort from transport_vsock.go (linux).
// Both must agree so the host-side dial port matches the guest-side listen port.
const vsockBasePortDarwin = 6000

// VZTransport implements Transport using the Virtualization.framework's
// vsock API on macOS. It wraps a VM's ConnectVsock method to dial
// per-mapping ports on the guest, mirroring what VsockTransport does
// on Linux via AF_VSOCK.
type VZTransport struct {
	VM interface {
		ConnectVsock(port uint32) (*os.File, error)
	}
}

func (t VZTransport) DialMapping(_ context.Context, id int) (net.Conn, error) {
	port := uint32(vsockBasePortDarwin + id)
	f, err := t.VM.ConnectVsock(port)
	if err != nil {
		return nil, fmt.Errorf("vz vsock connect port=%d: %w", port, err)
	}
	// Always use vzFileConn with blocking I/O. VZ framework vsock fds
	// are not regular sockets — net.FileConn sets non-blocking mode and
	// registers with kqueue, but the fd may not trigger kqueue readiness
	// events, causing io.Copy to hang indefinitely.
	return &vzFileConn{f: f}, nil
}

func (t VZTransport) Close() error { return nil }

// vzFileConn is a minimal net.Conn wrapper for *os.File on darwin.
type vzFileConn struct {
	f *os.File
}

func (c *vzFileConn) Read(b []byte) (int, error)         { return c.f.Read(b) }
func (c *vzFileConn) Write(b []byte) (int, error)        { return c.f.Write(b) }
func (c *vzFileConn) Close() error                       { return c.f.Close() }
func (c *vzFileConn) LocalAddr() net.Addr                { return vzAddr{} }
func (c *vzFileConn) RemoteAddr() net.Addr               { return vzAddr{} }
func (c *vzFileConn) SetDeadline(_ time.Time) error      { return nil }
func (c *vzFileConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *vzFileConn) SetWriteDeadline(_ time.Time) error { return nil }

type vzAddr struct{}

func (vzAddr) Network() string { return "vsock" }
func (vzAddr) String() string  { return "vsock" }
