//go:build linux

package console

import (
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// safeClose returns a function that closes ch exactly once.
func safeClose(ch chan struct{}) func() {
	var once sync.Once
	return func() {
		once.Do(func() { close(ch) })
	}
}

// Frame types for the console socket protocol.
const (
	frameData   byte = 0
	frameResize byte = 1
)

// StartSocket starts a Unix socket listener that proxies I/O to the PTY master.
// Only one client is served at a time. The listener persists for the container lifetime.
// Returns a cleanup function that closes the listener and removes the socket.
func StartSocket(name string, ptmx *os.File) (func(), error) {
	dir := Dir(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating console dir: %w", err)
	}

	sockPath := SocketPath(name)
	os.Remove(sockPath) // remove stale

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("listen console socket: %w", err)
	}

	// Write mode marker
	os.WriteFile(ModePath(name), []byte(ModeSocket), 0o644)

	go serveConsole(listener, ptmx, name)

	cleanup := func() {
		listener.Close()
		os.Remove(sockPath)
	}
	return cleanup, nil
}

// serveConsole runs a single PTY reader goroutine and accepts one client at a
// time. The PTY reader writes to whichever client is currently connected
// (mutex-protected). This avoids blocking Accept on a previous client's
// cleanup and prevents multiple goroutines from racing on ptmx.Read.
func serveConsole(listener net.Listener, ptmx *os.File, name string) {
	var (
		mu      sync.Mutex
		curConn net.Conn
	)

	// Permanent goroutine: PTY master → current client (data frames).
	// Runs for the lifetime of the container.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				frame := make([]byte, 1+2+n)
				frame[0] = frameData
				binary.BigEndian.PutUint16(frame[1:3], uint16(n))
				copy(frame[3:], buf[:n])

				mu.Lock()
				if curConn != nil {
					if _, werr := curConn.Write(frame); werr != nil {
						curConn = nil
					}
				}
				mu.Unlock()
			}
			if err != nil {
				return // PTY closed (container exited)
			}
		}
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			// Listener closed (container exiting)
			return
		}

		// Disconnect previous client if still lingering
		mu.Lock()
		if curConn != nil {
			curConn.Close()
		}
		curConn = conn
		mu.Unlock()

		slog.Debug("console", "action", "client attached", "container", name)

		// Client → PTY master (blocks until client disconnects)
		handleClientInput(conn, ptmx)

		// Client disconnected — clear current connection
		mu.Lock()
		if curConn == conn {
			curConn = nil
		}
		mu.Unlock()
		conn.Close()

		slog.Debug("console", "action", "client detached", "container", name)
	}
}

// handleClientInput reads framed messages from the client and writes to the PTY master.
// Returns when the client disconnects.
func handleClientInput(conn net.Conn, ptmx *os.File) {
	header := make([]byte, 3) // type(1) + len(2)
	for {
		if _, err := io.ReadFull(conn, header); err != nil {
			return // client disconnected
		}
		frameType := header[0]
		frameLen := binary.BigEndian.Uint16(header[1:3])

		payload := make([]byte, frameLen)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}

		switch frameType {
		case frameData:
			ptmx.Write(payload)
		case frameResize:
			if len(payload) == 4 {
				rows := binary.BigEndian.Uint16(payload[0:2])
				cols := binary.BigEndian.Uint16(payload[2:4])
				ws := unix.Winsize{Row: rows, Col: cols}
				unix.IoctlSetWinsize(int(ptmx.Fd()), unix.TIOCSWINSZ, &ws)
			}
		}
	}
}

// AttachSocket connects to a console socket and relays I/O with the host terminal.
// Sends host stdin as data frames, receives PTY output as data frames.
// Supports Ctrl+P,Ctrl+Q to detach without killing the container.
func AttachSocket(name string, hostStdin *os.File, hostStdout io.Writer, done <-chan struct{}) error {
	conn, err := net.Dial("unix", SocketPath(name))
	if err != nil {
		return fmt.Errorf("connect console socket: %w", err)
	}
	defer conn.Close()

	detach := make(chan struct{})
	closeDetach := safeClose(detach)

	// Socket → host stdout (read data frames)
	go func() {
		header := make([]byte, 3)
		for {
			if _, err := io.ReadFull(conn, header); err != nil {
				closeDetach()
				return
			}
			frameLen := binary.BigEndian.Uint16(header[1:3])
			payload := make([]byte, frameLen)
			if _, err := io.ReadFull(conn, payload); err != nil {
				closeDetach()
				return
			}
			if header[0] == frameData {
				hostStdout.Write(payload)
			}
		}
	}()

	// Host stdin → socket (data frames + detach detection)
	go func() {
		buf := make([]byte, 4096)
		var prevCtrlP bool
		for {
			n, err := hostStdin.Read(buf)
			if n > 0 {
				data := buf[:n]
				// Detect Ctrl+P, Ctrl+Q sequence for detach
				for i, b := range data {
					if prevCtrlP && b == 0x11 { // Ctrl+Q
						closeDetach()
						return
					}
					prevCtrlP = (b == 0x10) // Ctrl+P
					_ = i
				}

				frame := make([]byte, 1+2+n)
				frame[0] = frameData
				binary.BigEndian.PutUint16(frame[1:3], uint16(n))
				copy(frame[3:], data)
				if _, werr := conn.Write(frame); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-detach:
	}
	return nil
}

// SendResize sends a resize frame to the console socket.
func SendResize(conn net.Conn, rows, cols uint16) error {
	frame := make([]byte, 1+2+4)
	frame[0] = frameResize
	binary.BigEndian.PutUint16(frame[1:3], 4)
	binary.BigEndian.PutUint16(frame[3:5], rows)
	binary.BigEndian.PutUint16(frame[5:7], cols)
	_, err := conn.Write(frame)
	return err
}
