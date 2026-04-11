package forward

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

// Transport is the per-mapping dial interface used by the host listener to
// reach the in-container relay (native: unix socket, vm: AF_VSOCK).
type Transport interface {
	DialMapping(ctx context.Context, id int) (net.Conn, error)
	Close() error
}

// Start binds host listeners for all mappings and wires each accepted
// connection (or datagram flow) to transport.DialMapping(id). It returns a
// stop function that closes listeners and all in-flight relays.
func Start(ctx context.Context, containerName string, mappings []PortMapping, transport Transport) (func(), error) {
	if len(mappings) == 0 {
		return func() {}, nil
	}

	var tlsCert *tls.Certificate
	for _, m := range mappings {
		if m.Scheme == SchemeTLS {
			ips := []string{}
			if m.Host.Kind == KindNet && m.Host.IP != "" {
				ips = append(ips, m.Host.IP)
			}
			c, err := NewSelfSignedCert(containerName, ips)
			if err != nil {
				return nil, fmt.Errorf("tls cert: %w", err)
			}
			tlsCert = &c
			break
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	var closers []io.Closer
	var mu sync.Mutex
	add := func(c io.Closer) {
		mu.Lock()
		closers = append(closers, c)
		mu.Unlock()
	}

	for _, m := range mappings {
		m := m
		if err := startMapping(ctx, m, transport, tlsCert, add); err != nil {
			slog.Warn("forward: start mapping", slog.String("raw", m.Raw), slog.Any("err", err))
		}
	}

	stop := func() {
		cancel()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range closers {
			c.Close()
		}
	}
	return stop, nil
}

func startMapping(ctx context.Context, m PortMapping, transport Transport, tlsCert *tls.Certificate, add func(io.Closer)) error {
	switch m.Scheme {
	case SchemeUDP:
		return startDgram(ctx, m, transport, add)
	default:
		return startStream(ctx, m, transport, tlsCert, add)
	}
}

func startStream(ctx context.Context, m PortMapping, transport Transport, tlsCert *tls.Certificate, add func(io.Closer)) error {
	var l net.Listener
	var err error
	switch m.Host.Kind {
	case KindNet:
		l, err = net.Listen("tcp", net.JoinHostPort(m.Host.IP, strconv.Itoa(m.Host.Port)))
	case KindUnix:
		os.Remove(m.Host.Path)
		l, err = net.Listen("unix", m.Host.Path)
	default:
		return fmt.Errorf("unknown host kind %q", m.Host.Kind)
	}
	if err != nil {
		return err
	}
	if m.Scheme == SchemeTLS {
		if tlsCert == nil {
			l.Close()
			return fmt.Errorf("tls cert missing")
		}
		l = tls.NewListener(l, &tls.Config{Certificates: []tls.Certificate{*tlsCert}})
	}
	add(l)
	slog.Info("forward: listening",
		slog.String("scheme", string(m.Scheme)),
		slog.String("host", m.Host.String()),
		slog.String("cont", m.Cont.String()))

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go proxyStream(ctx, m, transport, c)
		}
	}()
	return nil
}

func proxyStream(ctx context.Context, m PortMapping, transport Transport, c net.Conn) {
	defer c.Close()
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	target, err := transport.DialMapping(dialCtx, m.ID)
	if err != nil {
		slog.Warn("forward: dial transport", slog.Int("id", m.ID), slog.Any("err", err))
		return
	}
	defer target.Close()
	pipe(c, target)
}

// pipe copies bytes bidirectionally between two stream connections and
// returns as soon as EITHER direction sees EOF or an error. Before
// returning it closes both connections, which forcibly unblocks any read
// that is still parked on the other direction — without this, a container
// process closing its end leaves the client hanging because our second
// io.Copy is still waiting on the client to send.
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { io.Copy(a, b); done <- struct{}{} }()
	go func() { io.Copy(b, a); done <- struct{}{} }()
	<-done
	a.Close()
	b.Close()
	<-done
}

// startDgram sets up a UDP or unix-dgram listener and per-source flow
// table. Each source peer gets its own target net.Conn obtained from the
// Transport. No length-prefix framing is used: exactly one Write per
// received datagram maps naturally to both possible target types:
//
//   - UDP target: one Write == one packet, preserving message boundaries.
//   - TCP / unix-stream target: Write appends bytes to the stream, which
//     is what a cross-protocol (udp→tcp) mapping is expected to do.
//
// The reverse direction symmetrically treats every target Read as one
// datagram back to the source peer.
func startDgram(ctx context.Context, m PortMapping, transport Transport, add func(io.Closer)) error {
	var (
		pc  net.PacketConn
		err error
	)
	switch m.Host.Kind {
	case KindNet:
		pc, err = net.ListenPacket("udp", net.JoinHostPort(m.Host.IP, strconv.Itoa(m.Host.Port)))
	case KindUnix:
		os.Remove(m.Host.Path)
		pc, err = net.ListenPacket("unixgram", m.Host.Path)
	default:
		return fmt.Errorf("unknown host kind %q", m.Host.Kind)
	}
	if err != nil {
		return err
	}
	add(pc)

	type flow struct {
		conn     net.Conn
		lastSeen time.Time
	}
	var (
		mu    sync.Mutex
		flows = map[string]*flow{}
	)

	go func() {
		buf := make([]byte, 65536)
		for {
			n, peer, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			key := peer.String()
			mu.Lock()
			f, ok := flows[key]
			if !ok {
				target, derr := transport.DialMapping(ctx, m.ID)
				if derr != nil {
					mu.Unlock()
					slog.Warn("forward: dgram dial", slog.Int("id", m.ID), slog.Any("err", derr))
					continue
				}
				f = &flow{conn: target, lastSeen: time.Now()}
				flows[key] = f
				go func(f *flow, peer net.Addr, key string) {
					defer func() {
						f.conn.Close()
						mu.Lock()
						delete(flows, key)
						mu.Unlock()
					}()
					rbuf := make([]byte, 65536)
					for {
						n, err := f.conn.Read(rbuf)
						if n > 0 {
							pc.WriteTo(rbuf[:n], peer)
						}
						if err != nil {
							return
						}
					}
				}(f, peer, key)
			}
			f.lastSeen = time.Now()
			mu.Unlock()

			f.conn.Write(buf[:n])
		}
	}()
	return nil
}

// AssignIDs assigns stable sequential IDs to a mapping slice in place.
// Called once when mappings are parsed.
func AssignIDs(mappings []PortMapping) {
	for i := range mappings {
		mappings[i].ID = i
	}
}
