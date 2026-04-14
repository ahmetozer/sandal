package forward

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
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

	var startErrs []error
	for _, m := range mappings {
		m := m
		if err := startMapping(ctx, m, transport, tlsCert, add); err != nil {
			slog.Warn("forward: start mapping", slog.String("raw", m.Raw), slog.Any("err", err))
			startErrs = append(startErrs, fmt.Errorf("%s: %w", m.Raw, err))
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
	if len(startErrs) > 0 {
		// Roll back any partial listeners so we don't leave orphan binds.
		stop()
		return func() {}, errors.Join(startErrs...)
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

// pipe copies bytes bidirectionally between two stream connections.
//
// It uses half-close semantics: when one direction sees EOF, it calls
// CloseWrite on the destination to propagate the shutdown without
// destroying the fd. This lets io.Copy run uninterrupted for its full
// duration, which is critical for the splice(2) zero-copy path — the
// previous implementation closed both connections on first-EOF, which
// could interrupt an in-flight splice in the other direction.
//
// For connection types that don't support CloseWrite (vsock fileConn,
// some unix sockets), it falls back to a full Close which unblocks the
// other direction immediately.
func pipe(a, b net.Conn) {
	done := make(chan struct{})
	go func() {
		io.Copy(a, b)
		halfClose(a)
		close(done)
	}()
	io.Copy(b, a)
	halfClose(b)
	<-done
	a.Close()
	b.Close()
}

// halfClose sends a write-shutdown (FIN for TCP) without closing the
// read side, so the peer sees EOF while the reverse direction can still
// drain. Falls back to full Close if the conn doesn't support it.
func halfClose(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	} else {
		c.Close()
	}
}

// startDgram sets up a UDP or unix-dgram listener and a sharded flow
// table that maps each source peer to a container-side connection.
//
// Performance notes:
//   - The flow table is sharded into flowShards buckets to reduce mutex
//     contention at high PPS (each bucket has its own lock).
//   - Read buffers are pooled via sync.Pool to avoid per-packet allocation.
//   - A background reaper goroutine evicts flows idle for >60s.
//   - One Write per received datagram; no length-prefix framing. This maps
//     naturally to UDP targets (one packet) and TCP targets (stream append).
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

	ft := newFlowTable()

	// Reaper: evict idle flows every 30s.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ft.evictIdle(flowIdleTimeout)
			}
		}
	}()

	// Read loop.
	go func() {
		buf := make([]byte, 65536)
		for {
			n, peer, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			key := peer.String()
			f := ft.get(key)
			if f == nil {
				target, derr := transport.DialMapping(ctx, m.ID)
				if derr != nil {
					slog.Warn("forward: dgram dial", slog.Int("id", m.ID), slog.Any("err", derr))
					continue
				}
				f = ft.getOrCreate(key, target, func() {
					// Reply reader goroutine: target → source peer.
					readBuf := bufPool.Get().([]byte)
					defer bufPool.Put(readBuf)
					for {
						rn, rerr := target.Read(readBuf)
						if rn > 0 {
							pc.WriteTo(readBuf[:rn], peer)
						}
						if rerr != nil {
							return
						}
					}
				})
				if f == nil {
					// We created the entry. target is now owned by the
					// flow table and read by the reply goroutine.
					// Retrieve the flow for the Write below.
					f = ft.get(key)
					if f == nil {
						continue
					}
				} else {
					// Key already existed; our dialed target is unused.
					target.Close()
				}
			}
			f.touch()
			f.conn.Write(buf[:n])
		}
	}()
	return nil
}

// ---------- sharded flow table ----------

const (
	flowShards      = 64
	flowIdleTimeout = 60 * time.Second
)

var bufPool = sync.Pool{
	New: func() any { return make([]byte, 65536) },
}

type udpFlow struct {
	conn     net.Conn
	lastSeen atomic.Int64 // unix nano
}

func (f *udpFlow) touch() { f.lastSeen.Store(time.Now().UnixNano()) }

type flowShard struct {
	mu    sync.Mutex
	flows map[string]*udpFlow
}

type flowTable struct {
	shards [flowShards]flowShard
}

func newFlowTable() *flowTable {
	ft := &flowTable{}
	for i := range ft.shards {
		ft.shards[i].flows = make(map[string]*udpFlow)
	}
	return ft
}

func (ft *flowTable) shard(key string) *flowShard {
	h := uint32(0)
	for i := 0; i < len(key); i++ {
		h = h*31 + uint32(key[i])
	}
	return &ft.shards[h%flowShards]
}

func (ft *flowTable) get(key string) *udpFlow {
	s := ft.shard(key)
	s.mu.Lock()
	f := s.flows[key]
	s.mu.Unlock()
	return f
}

// getOrCreate atomically inserts a new flow if the key doesn't exist yet.
// If it was inserted, it spawns readFn as a goroutine. Returns nil if the
// key was already present (caller should discard its pre-dialed conn).
func (ft *flowTable) getOrCreate(key string, conn net.Conn, readFn func()) *udpFlow {
	s := ft.shard(key)
	s.mu.Lock()
	if existing, ok := s.flows[key]; ok {
		s.mu.Unlock()
		return existing
	}
	f := &udpFlow{conn: conn}
	f.touch()
	s.flows[key] = f
	s.mu.Unlock()
	go func() {
		defer func() {
			conn.Close()
			s.mu.Lock()
			delete(s.flows, key)
			s.mu.Unlock()
		}()
		readFn()
	}()
	return nil // signals "we created it"
}

func (ft *flowTable) evictIdle(maxIdle time.Duration) {
	cutoff := time.Now().Add(-maxIdle).UnixNano()
	for i := range ft.shards {
		s := &ft.shards[i]
		s.mu.Lock()
		for key, f := range s.flows {
			if f.lastSeen.Load() < cutoff {
				f.conn.Close()
				delete(s.flows, key)
			}
		}
		s.mu.Unlock()
	}
}

// AssignIDs assigns stable sequential IDs to a mapping slice in place.
// Called once when mappings are parsed.
func AssignIDs(mappings []PortMapping) {
	for i := range mappings {
		mappings[i].ID = i
	}
}
