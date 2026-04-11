package forward

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"time"
)

// RelayEntry is the serialized form passed to the in-namespace relay via
// environment variable. Transports set UnixPath (native) or VsockPort (vm).
type RelayEntry struct {
	ID        int    `json:"id"`
	Kind      string `json:"kind"`   // "port" | "unix"
	Port      int    `json:"port,omitempty"`
	Path      string `json:"path,omitempty"`
	Proto     string `json:"proto"` // "tcp" | "udp"
	UnixPath  string `json:"unix_path,omitempty"`
	VsockPort uint32 `json:"vsock_port,omitempty"`
}

// RelayEntries wraps a slice for JSON-base64 round-trip.
type RelayEntries []RelayEntry

// EncodeEntries returns a base64 JSON representation for environment variables.
func EncodeEntries(entries RelayEntries) (string, error) {
	b, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// DecodeEntries parses the JSON produced by EncodeEntries.
func DecodeEntries(s string) (RelayEntries, error) {
	if s == "" {
		return nil, nil
	}
	var out RelayEntries
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Listener is the subset of net.Listener we need in the relay body.
type Listener interface {
	Accept() (net.Conn, error)
	Close() error
}

// ListenFunc returns a listener that receives host->target traffic for the
// given entry. Implementations: unix socket (native), AF_VSOCK (vm).
type ListenFunc func(e RelayEntry) (Listener, error)

// RunRelay starts one accept loop per entry. It is shared between the
// native in-netns helper and the VM guest init.
func RunRelay(entries RelayEntries, listen ListenFunc) error {
	if len(entries) == 0 {
		return nil
	}
	for _, e := range entries {
		l, err := listen(e)
		if err != nil {
			slog.Warn("forward: listen", slog.Int("id", e.ID), slog.Any("err", err))
			continue
		}
		go acceptLoop(e, l)
	}
	return nil
}

func acceptLoop(e RelayEntry, l Listener) {
	defer l.Close()
	for {
		c, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			slog.Warn("forward: accept", slog.Int("id", e.ID), slog.Any("err", err))
			return
		}
		go handleConn(e, c)
	}
}

func handleConn(e RelayEntry, c net.Conn) {
	defer c.Close()

	if e.Proto == "udp" {
		handleDgram(e, c)
		return
	}

	target, err := dialTarget(e)
	if err != nil {
		slog.Warn("forward: dial target", slog.Int("id", e.ID), slog.Any("err", err))
		return
	}
	defer target.Close()

	done := make(chan struct{})
	go func() {
		io.Copy(target, c)
		if tc, ok := target.(interface{ CloseWrite() error }); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}()
	io.Copy(c, target)
	<-done
}

// dialTarget opens a connection to the actual container-side target.
// Runs inside the container's namespaces, so 127.0.0.1 and local unix
// paths resolve correctly.
func dialTarget(e RelayEntry) (net.Conn, error) {
	switch e.Kind {
	case "port":
		proto := e.Proto
		if proto == "" {
			proto = "tcp"
		}
		return net.Dial(proto, fmt.Sprintf("127.0.0.1:%d", e.Port))
	case "unix":
		network := "unix"
		if e.Proto == "udp" {
			network = "unixgram"
		}
		return net.Dial(network, e.Path)
	default:
		return nil, fmt.Errorf("unknown kind %q", e.Kind)
	}
}

// handleDgram bridges datagram traffic. The stream carries length-prefixed
// datagrams: [uint16 len][payload]. One connection per source flow, with
// an idle timeout.
func handleDgram(e RelayEntry, c net.Conn) {
	target, err := dialTarget(e)
	if err != nil {
		slog.Warn("forward: dial udp target", slog.Int("id", e.ID), slog.Any("err", err))
		return
	}
	defer target.Close()

	const idle = 60 * time.Second

	// stream -> target (unframe, sendto)
	go func() {
		var hdr [2]byte
		for {
			c.SetReadDeadline(time.Now().Add(idle))
			if _, err := io.ReadFull(c, hdr[:]); err != nil {
				return
			}
			n := int(binary.BigEndian.Uint16(hdr[:]))
			buf := make([]byte, n)
			if _, err := io.ReadFull(c, buf); err != nil {
				return
			}
			target.Write(buf)
		}
	}()

	// target -> stream (recv, frame)
	buf := make([]byte, 65536)
	for {
		target.SetReadDeadline(time.Now().Add(idle))
		n, err := target.Read(buf)
		if err != nil {
			return
		}
		var hdr [2]byte
		binary.BigEndian.PutUint16(hdr[:], uint16(n))
		if _, err := c.Write(hdr[:]); err != nil {
			return
		}
		if _, err := c.Write(buf[:n]); err != nil {
			return
		}
	}
}

// RelayEnvVar is the environment variable name that carries the JSON-encoded
// relay config to the in-namespace relay process (native helper) and the
// VM guest init.
const RelayEnvVar = "SANDAL_FORWARDS"

// LoadEntriesFromEnv reads and parses RelayEnvVar.
func LoadEntriesFromEnv() (RelayEntries, error) {
	return DecodeEntries(os.Getenv(RelayEnvVar))
}
