package forward

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// RendezvousDirInContainer is the path inside the container where the in-netns
// helper creates its unix listening sockets. The host dials the same inodes via
// /proc/<contPid>/root/<RendezvousDirInContainer>.
const RendezvousDirInContainer = "/tmp/.sandal-forwards"

// NativeTransport implements Transport using unix sockets that the in-netns
// helper binds inside the container's mount namespace. The host reaches them
// via /proc/<contPid>/root/.
type NativeTransport struct {
	ContPid int
}

func (t NativeTransport) DialMapping(_ context.Context, id int) (net.Conn, error) {
	path := fmt.Sprintf("/proc/%d/root%s/%d.sock", t.ContPid, RendezvousDirInContainer, id)
	return net.Dial("unix", path)
}

func (t NativeTransport) Close() error { return nil }

// NativeListen is the ListenFunc used by the in-netns helper.
func NativeListen(e RelayEntry) (Listener, error) {
	dir := RendezvousDirInContainer
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("%d.sock", e.ID))
	os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	return l, nil
}

// BuildNativeEntries converts PortMapping list into RelayEntry list for the
// native helper. The unix transport path is set per entry.
func BuildNativeEntries(mappings []PortMapping) RelayEntries {
	entries := make(RelayEntries, 0, len(mappings))
	for _, m := range mappings {
		e := RelayEntry{
			ID:       m.ID,
			Proto:    protoOf(m.Scheme),
			UnixPath: fmt.Sprintf("%s/%d.sock", RendezvousDirInContainer, m.ID),
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

// protoOf maps a flag Scheme to the wire protocol the target dial should use.
// tls is terminated on the host; the tunnel carries plaintext stream.
func protoOf(s Scheme) string {
	if s == SchemeUDP {
		return "udp"
	}
	return "tcp"
}
