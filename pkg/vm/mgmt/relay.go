package mgmt

import (
	"io"
	"log/slog"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Connector abstracts vsock connection creation.
// KVM uses AF_VSOCK dial, VZ uses ConnectVsock method.
type Connector interface {
	Connect() (io.ReadWriteCloser, error)
}

// StartManagementSocket creates a per-container Unix socket on the host and
// relays each accepted connection to the guest's embedded controller via the
// provided Connector. Must be called as a goroutine.
// Returns a cleanup function that closes the listener and removes the socket.
func StartManagementSocket(name string, connector Connector) func() {
	sockDir := SocketDir()
	os.MkdirAll(sockDir, 0o755)
	sockPath := SocketPath(name)
	os.Remove(sockPath) // clean stale

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		slog.Warn("management socket: listen", slog.String("path", sockPath), slog.Any("err", err))
		return func() {}
	}

	cleanup := func() {
		ln.Close()
		os.Remove(sockPath)
	}

	slog.Debug("management socket ready", slog.String("path", sockPath))

	// Brief wait for the guest controller to be ready before accepting.
	time.Sleep(500 * time.Millisecond)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				slog.Debug("management socket: accept done", slog.Any("err", err))
				return
			}
			go managementRelay(connector, conn)
		}
	}()

	return cleanup
}

// managementRelay connects a host-side client to the guest's embedded
// controller and performs bidirectional relay.
func managementRelay(connector Connector, hostConn net.Conn) {
	guestConn, err := connector.Connect()
	if err != nil {
		slog.Warn("management relay: connect", slog.Any("err", err))
		hostConn.Close()
		return
	}
	slog.Debug("management relay: connected")

	// AF_VSOCK fds returned by Connect() are *os.File-wrapped raw fds; Go's
	// runtime netpoller does not manage them, so a parked Read() is not
	// woken by a sibling Close(). Without an explicit shutdown(2), closing
	// the vsock side here does not propagate FIN to the peer until the
	// orphaned read finally returns — which never happens until the user
	// types something. Force-shutdown via the fd if available.
	closeGuest := func() {
		if f, ok := guestConn.(interface{ Fd() uintptr }); ok {
			unix.Shutdown(int(f.Fd()), unix.SHUT_RDWR)
		}
		guestConn.Close()
	}

	// Wrap connections to prevent io.Copy from using splice/sendfile,
	// which can batch data and delay individual keystrokes in interactive
	// sessions. The wrapper forces io.Copy to use userspace read/write.

	// host → guest
	go func() {
		io.Copy(writerOnly{guestConn}, readerOnly{hostConn})
		closeGuest()
	}()
	// guest → host
	io.Copy(writerOnly{hostConn}, readerOnly{guestConn})
	closeGuest()
	hostConn.Close()
}

// readerOnly hides WriteTo so io.Copy can't use splice/sendfile.
type readerOnly struct{ io.Reader }

// writerOnly hides ReadFrom so io.Copy can't use splice/sendfile.
type writerOnly struct{ io.Writer }
