package forward

import (
	"context"
	"crypto/tls"
	"encoding/binary"
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

	done := make(chan struct{})
	go func() {
		io.Copy(target, c)
		done <- struct{}{}
	}()
	io.Copy(c, target)
	<-done
}

// startDgram sets up a UDP or unix-dgram listener and per-source flow table.
// Each flow opens a dedicated transport stream and length-prefixes datagrams.
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
		stream   net.Conn
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
				f = &flow{stream: target, lastSeen: time.Now()}
				flows[key] = f
				// reader: frame reply -> pc.WriteTo(peer)
				go func(f *flow, peer net.Addr, key string) {
					defer func() {
						f.stream.Close()
						mu.Lock()
						delete(flows, key)
						mu.Unlock()
					}()
					var hdr [2]byte
					for {
						if _, err := io.ReadFull(f.stream, hdr[:]); err != nil {
							return
						}
						n := int(binary.BigEndian.Uint16(hdr[:]))
						b := make([]byte, n)
						if _, err := io.ReadFull(f.stream, b); err != nil {
							return
						}
						pc.WriteTo(b, peer)
					}
				}(f, peer, key)
			}
			f.lastSeen = time.Now()
			mu.Unlock()

			var hdr [2]byte
			binary.BigEndian.PutUint16(hdr[:], uint16(n))
			f.stream.Write(hdr[:])
			f.stream.Write(buf[:n])
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
