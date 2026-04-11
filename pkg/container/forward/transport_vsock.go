//go:build linux

package forward

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// VsockBasePort is the first vsock port used for port-forwarding mappings.
// Chosen to avoid collisions with existing sandal vsock ports:
//   - 4000 management/controller
//   - 5000-5099 socket-relay (-v)
const VsockBasePort = 6000

// VsockTransport implements Transport using AF_VSOCK to reach a guest relay.
type VsockTransport struct {
	GuestCID uint32
}

func (t VsockTransport) DialMapping(_ context.Context, id int) (net.Conn, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("vsock socket: %w", err)
	}
	port := uint32(VsockBasePort + id)
	if err := unix.Connect(fd, &unix.SockaddrVM{CID: t.GuestCID, Port: port}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("vsock connect cid=%d port=%d: %w", t.GuestCID, port, err)
	}
	f := os.NewFile(uintptr(fd), fmt.Sprintf("vsock:%d:%d", t.GuestCID, port))
	return &fileConn{f: f, fd: fd}, nil
}

func (t VsockTransport) Close() error { return nil }

// fileConn wraps an *os.File as a net.Conn so io.Copy works.
type fileConn struct {
	f  *os.File
	fd int
}

func (c *fileConn) Read(b []byte) (int, error)  { return c.f.Read(b) }
func (c *fileConn) Write(b []byte) (int, error) { return c.f.Write(b) }
func (c *fileConn) Close() error {
	unix.Shutdown(c.fd, unix.SHUT_RDWR)
	return c.f.Close()
}
func (c *fileConn) LocalAddr() net.Addr                { return vsockAddr{} }
func (c *fileConn) RemoteAddr() net.Addr               { return vsockAddr{} }
func (c *fileConn) SetDeadline(_ time.Time) error      { return nil }
func (c *fileConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *fileConn) SetWriteDeadline(_ time.Time) error { return nil }

type vsockAddr struct{}

func (vsockAddr) Network() string { return "vsock" }
func (vsockAddr) String() string  { return "vsock" }

// VsockListen is the ListenFunc used by the VM guest relay.
func VsockListen(e RelayEntry) (Listener, error) {
	port := e.VsockPort
	if port == 0 {
		port = uint32(VsockBasePort + e.ID)
	}
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("vsock socket: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("vsock bind port=%d: %w", port, err)
	}
	if err := unix.Listen(fd, 8); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("vsock listen port=%d: %w", port, err)
	}
	return &vsockListener{fd: fd, port: port}, nil
}

type vsockListener struct {
	fd   int
	port uint32
}

func (l *vsockListener) Accept() (net.Conn, error) {
	nfd, _, err := unix.Accept(l.fd)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(nfd), fmt.Sprintf("vsock-accepted:%d", l.port))
	return &fileConn{f: f, fd: nfd}, nil
}

func (l *vsockListener) Close() error {
	unix.Shutdown(l.fd, unix.SHUT_RDWR)
	return unix.Close(l.fd)
}

// protoOf maps a flag Scheme to the wire protocol used inside the container
// when dialing the target. TLS is terminated on the host; the tunnel carries
// plaintext stream.
func protoOf(s Scheme) string {
	if s == SchemeUDP {
		return "udp"
	}
	return "tcp"
}

// BuildVsockEntries converts PortMapping list into RelayEntry list for the
// VM guest relay. The vsock port is allocated sequentially from VsockBasePort.
func BuildVsockEntries(mappings []PortMapping) RelayEntries {
	entries := make(RelayEntries, 0, len(mappings))
	for _, m := range mappings {
		e := RelayEntry{
			ID:        m.ID,
			Proto:     protoOf(m.Scheme),
			VsockPort: uint32(VsockBasePort + m.ID),
		}
		if m.Cont.Kind == KindNet {
			e.Kind = "port"
			e.Port = m.Cont.Port
		} else {
			e.Kind = "unix"
			e.Path = m.Cont.Path
		}
		entries = append(entries, e)
	}
	return entries
}
