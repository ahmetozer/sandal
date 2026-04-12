//go:build linux

package forward

import (
	"context"
	"fmt"
	"log/slog"
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
	// Try net.FileConn for netpoller integration + splice(2) zero-copy.
	// This runs on the KVM host with a real Linux kernel that typically
	// supports vsock epoll. Falls back to blocking fileConn if it fails.
	if nc, err := net.FileConn(f); err == nil {
		f.Close() // net.FileConn dups the fd; close our original
		return nc, nil
	}
	return &fileConn{f: f, fd: fd}, nil
}

func (t VsockTransport) Close() error { return nil }

// fileConn wraps an *os.File as a net.Conn. It implements deadlines via
// SO_RCVTIMEO/SO_SNDTIMEO so that a stuck container unblocks the relay
// goroutine instead of leaking it forever.
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
func (c *fileConn) LocalAddr() net.Addr  { return vsockAddr{} }
func (c *fileConn) RemoteAddr() net.Addr { return vsockAddr{} }

func (c *fileConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

func (c *fileConn) SetReadDeadline(t time.Time) error {
	return c.setSockTimeout(unix.SO_RCVTIMEO, t)
}

func (c *fileConn) SetWriteDeadline(t time.Time) error {
	return c.setSockTimeout(unix.SO_SNDTIMEO, t)
}

func (c *fileConn) setSockTimeout(opt int, t time.Time) error {
	var tv unix.Timeval
	if !t.IsZero() {
		d := time.Until(t)
		if d <= 0 {
			d = 1 // minimum 1µs to force immediate timeout
		}
		tv = unix.NsecToTimeval(d.Nanoseconds())
	}
	// Zero tv clears the timeout (blocks indefinitely).
	return unix.SetsockoptTimeval(c.fd, unix.SOL_SOCKET, opt, &tv)
}

type vsockAddr struct{}

func (vsockAddr) Network() string { return "vsock" }
func (vsockAddr) String() string  { return "vsock" }

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
	// Always use blocking fileConn. net.FileConn sets non-blocking mode
	// and registers with epoll, but AF_VSOCK fds do not generate epoll
	// readiness events on many kernel versions. This causes io.Copy to
	// fail immediately with EAGAIN ("resource temporarily unavailable")
	// and zero bytes transferred.
	return &fileConn{f: f, fd: nfd}, nil
}

func (l *vsockListener) Close() error {
	unix.Shutdown(l.fd, unix.SHUT_RDWR)
	return unix.Close(l.fd)
}

// StartVsock creates one AF_VSOCK listener per mapping on VMADDR_CID_ANY
// at port VsockBasePort+id and relays each accepted connection through
// transport.DialMapping. Used inside a VM guest where the incoming side is
// vsock (from the physical host) and the target side is reached through
// a NetnsDialer setns'd into the in-VM container.
//
// It is the VM-guest counterpart of host.Start: same relay semantics, only
// the listener type differs.
func StartVsock(ctx context.Context, _ string, mappings []PortMapping, transport Transport) (func(), error) {
	if len(mappings) == 0 {
		return func() {}, nil
	}
	ctx, cancel := context.WithCancel(ctx)

	var listeners []*vsockListener
	stop := func() {
		cancel()
		for _, l := range listeners {
			l.Close()
		}
	}

	for _, m := range mappings {
		m := m
		port := uint32(VsockBasePort + m.ID)
		fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
		if err != nil {
			stop()
			return nil, fmt.Errorf("vsock socket: %w", err)
		}
		if err := unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port}); err != nil {
			unix.Close(fd)
			stop()
			return nil, fmt.Errorf("vsock bind port=%d: %w", port, err)
		}
		if err := unix.Listen(fd, 8); err != nil {
			unix.Close(fd)
			stop()
			return nil, fmt.Errorf("vsock listen port=%d: %w", port, err)
		}
		l := &vsockListener{fd: fd, port: port}
		listeners = append(listeners, l)
		go vsockAcceptLoop(ctx, m, l, transport)
	}
	return stop, nil
}

// vsockAcceptLoop drains accepts from a single vsock listener and hands
// each accepted fd off to a per-connection relay goroutine.
func vsockAcceptLoop(ctx context.Context, m PortMapping, l *vsockListener, transport Transport) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go vsockProxy(ctx, m, c, transport)
	}
}

// vsockProxy copies bytes bidirectionally between an accepted vsock
// connection and a container-side connection obtained from transport.
// Uses pipe() so a half-close on either end unblocks the other direction.
func vsockProxy(ctx context.Context, m PortMapping, vconn net.Conn, transport Transport) {
	defer vconn.Close()
	target, err := transport.DialMapping(ctx, m.ID)
	if err != nil {
		slog.Warn("forward: vsock proxy dial", slog.Int("id", m.ID), slog.Any("err", err))
		return
	}
	defer target.Close()
	pipe(vconn, target)
}

