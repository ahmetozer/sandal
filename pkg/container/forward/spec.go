// Package forward implements -p port forwarding for sandal.
//
// See .docs/port-forwarding.md for the full design.
package forward

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

type Scheme string

const (
	SchemeTCP Scheme = "tcp"
	SchemeUDP Scheme = "udp"
	SchemeTLS Scheme = "tls"
)

type EndpointKind string

const (
	KindNet  EndpointKind = "net"
	KindUnix EndpointKind = "unix"
)

// Endpoint is either a network address (ip:port) or a unix socket path.
type Endpoint struct {
	Kind EndpointKind
	IP   string
	Port int
	Path string
}

func (e Endpoint) String() string {
	if e.Kind == KindUnix {
		return "unix://" + e.Path
	}
	if e.IP == "" {
		return strconv.Itoa(e.Port)
	}
	return net.JoinHostPort(e.IP, strconv.Itoa(e.Port))
}

// PortMapping describes a single -p flag.
type PortMapping struct {
	ID     int
	Raw    string
	Scheme Scheme
	Host   Endpoint
	Cont   Endpoint
}

func (p PortMapping) String() string {
	return fmt.Sprintf("%s://%s->%s", p.Scheme, p.Host, p.Cont)
}

// ParseFlag parses a single -p value.
func ParseFlag(raw string) (PortMapping, error) {
	pm := PortMapping{Raw: raw, Scheme: SchemeTCP}

	s := raw
	// Scheme prefix is only recognized at the very start of the input,
	// otherwise "://" in a later unix:// token would confuse parsing.
	for _, p := range []struct {
		prefix string
		scheme Scheme
	}{
		{"tcp://", SchemeTCP},
		{"udp://", SchemeUDP},
		{"tls://", SchemeTLS},
	} {
		if rest, ok := strings.CutPrefix(s, p.prefix); ok {
			pm.Scheme = p.scheme
			s = rest
			break
		}
	}

	hostPart, contPart, err := splitHostCont(s)
	if err != nil {
		return pm, err
	}

	pm.Host, err = parseEndpoint(hostPart, true)
	if err != nil {
		return pm, fmt.Errorf("host endpoint: %w", err)
	}

	if contPart == "" {
		if pm.Host.Kind != KindNet {
			return pm, fmt.Errorf("container endpoint required when host endpoint is a unix socket")
		}
		pm.Cont = Endpoint{Kind: KindNet, IP: "127.0.0.1", Port: pm.Host.Port}
	} else {
		pm.Cont, err = parseEndpoint(contPart, false)
		if err != nil {
			return pm, fmt.Errorf("container endpoint: %w", err)
		}
		if pm.Cont.Kind == KindNet && pm.Cont.IP == "" {
			pm.Cont.IP = "127.0.0.1"
		}
	}

	if pm.Scheme == SchemeTLS && pm.Host.Kind == KindNet && pm.Host.IP == "" {
		pm.Host.IP = "127.0.0.1"
	}
	if pm.Host.Kind == KindNet && pm.Host.IP == "" {
		pm.Host.IP = "127.0.0.1"
	}

	return pm, nil
}

// splitHostCont splits host-endpoint and container-endpoint. It keeps
// "unix://..." tokens whole so their own colons and slashes are not parsed.
func splitHostCont(s string) (string, string, error) {
	if s == "" {
		return "", "", fmt.Errorf("empty mapping")
	}
	// If the whole thing is a single unix:// token, it's host-only.
	if strings.HasPrefix(s, "unix://") {
		// Host is unix; the container side starts after the ":unix://" or ":<port>"
		// that follows the unix path. We scan for the first ":" that is not
		// inside the unix path. unix paths end at end-of-string or at a
		// separator we introduce. The unambiguous separator is ":" followed
		// by either a digit, "tcp://", "udp://", "tls://", or "unix://".
		host, rest := splitAfterUnix(s)
		return host, rest, nil
	}
	// Host is net. Find ":unix://" or split at last ":" pair.
	if i := strings.Index(s, ":unix://"); i >= 0 {
		return s[:i], s[i+1:], nil
	}
	// No unix in container side — use raw tokens.
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		return parts[0], "", nil
	case 2:
		// Could be ip:port (host only) or port:port (host-port default-ip, cont-port).
		// If first part is numeric, it's port:port; else ip:port.
		if _, err := strconv.Atoi(parts[0]); err == nil {
			return parts[0], parts[1], nil
		}
		return s, "", nil
	case 3:
		return parts[0] + ":" + parts[1], parts[2], nil
	default:
		return "", "", fmt.Errorf("too many colons in %q", s)
	}
}

// splitAfterUnix returns the unix:// host endpoint and anything that follows it
// after a ":" separator. The container side must be an unambiguous token:
// either "tcp://", "udp://", "tls://", "unix://", or a trailing ":<digits>$".
func splitAfterUnix(s string) (string, string) {
	tokens := []string{":tcp://", ":udp://", ":tls://", ":unix://"}
	best := -1
	for _, t := range tokens {
		if i := strings.Index(s, t); i >= 0 && (best == -1 || i < best) {
			best = i
		}
	}
	// Trailing ":<digits>$" — target is a bare port.
	if i := strings.LastIndexByte(s, ':'); i >= 0 && i+1 < len(s) {
		allDigits := true
		for _, c := range s[i+1:] {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if allDigits && (best == -1 || i < best) {
			best = i
		}
	}
	if best == -1 {
		return s, ""
	}
	return s[:best], s[best+1:]
}

func parseEndpoint(s string, allowPortOnly bool) (Endpoint, error) {
	if path, ok := strings.CutPrefix(s, "unix://"); ok {
		if path == "" || path[0] != '/' {
			return Endpoint{}, fmt.Errorf("unix path must be absolute: %q", s)
		}
		return Endpoint{Kind: KindUnix, Path: path}, nil
	}
	// net endpoint: "<port>" or "<ip>:<port>" or "[ipv6]:port"
	if allowPortOnly {
		if p, err := strconv.Atoi(s); err == nil {
			return Endpoint{Kind: KindNet, Port: p}, nil
		}
	}
	host, portStr, err := net.SplitHostPort(s)
	if err != nil {
		// Try bare port for container side too.
		if p, err2 := strconv.Atoi(s); err2 == nil {
			return Endpoint{Kind: KindNet, Port: p}, nil
		}
		return Endpoint{}, fmt.Errorf("parse %q: %w", s, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return Endpoint{}, fmt.Errorf("parse port %q: %w", portStr, err)
	}
	return Endpoint{Kind: KindNet, IP: host, Port: port}, nil
}

// Validate returns an error if the combination of scheme/endpoints is invalid.
func (p PortMapping) Validate() error {
	if p.Scheme == SchemeTLS && p.Host.Kind == KindNet && p.Host.Port == 0 {
		return fmt.Errorf("tls mapping must have a host port")
	}
	if p.Scheme == "" {
		return fmt.Errorf("empty scheme")
	}
	return nil
}
