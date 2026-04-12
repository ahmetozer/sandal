//go:build linux

package forward

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"

	"github.com/ahmetozer/sandal/pkg/container/namespace"
)

// containerNetMntNS returns a Namespaces map targeting just the net and
// mnt namespaces of pid. This is the minimum set the port-forward worker
// needs so that net.Dial("tcp", "127.0.0.1:X") hits the container's
// loopback and net.Dial("unix", path) resolves against the container's
// mount namespace.
func containerNetMntNS(pid int) namespace.Namespaces {
	val := fmt.Sprintf("pid:%d", pid)
	ns := make(namespace.Namespaces, 2)
	v1, v2 := val, val
	ns["net"] = namespace.NamespaceConf{UserValue: &v1, IsUserDefined: true}
	ns["mnt"] = namespace.NamespaceConf{UserValue: &v2, IsUserDefined: true}
	return ns
}

// NetnsDialer is a Transport implementation for native (non-VM) containers.
//
// It pins N goroutines to dedicated OS threads (one per worker), each
// setns'd into the target container's network and mount namespaces. All
// workers share a single request channel so DialMapping calls are
// distributed across the pool. Because only socket(2)/connect(2) consult
// the calling thread's namespaces, subsequent read/write on the returned
// net.Conn runs on any thread without issue.
//
// Lifecycle: workers run until Close() is called. They never call
// UnlockOSThread, so when a worker returns the Go runtime destroys the
// tainted thread instead of reusing it.
type NetnsDialer struct {
	mappings  []PortMapping
	reqCh     chan netnsDialReq
	doneCh    chan struct{}
	poolSize  int
}

type netnsDialReq struct {
	id    int
	reply chan netnsDialReply
}

type netnsDialReply struct {
	conn net.Conn
	err  error
}

// dialerPoolSize returns the number of setns'd worker threads to use.
func dialerPoolSize() int {
	n := runtime.NumCPU()
	if n > 8 {
		n = 8
	}
	if n < 1 {
		n = 1
	}
	return n
}

// StartNetnsDialer creates a NetnsDialer that can reach the container whose
// init process is at contPid. It starts a pool of worker goroutines, each
// pinned to its own OS thread and setns'd into the container. Blocks until
// at least one worker has successfully entered the namespaces.
func StartNetnsDialer(contPid int, mappings []PortMapping) (*NetnsDialer, error) {
	poolSize := dialerPoolSize()
	d := &NetnsDialer{
		mappings: mappings,
		reqCh:    make(chan netnsDialReq, poolSize),
		doneCh:   make(chan struct{}),
		poolSize: poolSize,
	}
	// Start all workers. We need at least one to succeed.
	ready := make(chan error, poolSize)
	for i := 0; i < poolSize; i++ {
		go d.worker(contPid, ready)
	}
	// Wait for at least one successful worker.
	var firstErr error
	started := 0
	for i := 0; i < poolSize; i++ {
		if err := <-ready; err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else {
			started++
		}
	}
	if started == 0 {
		return nil, firstErr
	}
	return d, nil
}

// worker runs on a dedicated OS thread that is setns'd into the target
// container. Multiple workers share d.reqCh so DialMapping calls are
// distributed across the pool.
func (d *NetnsDialer) worker(contPid int, ready chan<- error) {
	runtime.LockOSThread()
	// No UnlockOSThread: the thread is retired after use.

	if err := namespace.Enter(contPid, containerNetMntNS(contPid)); err != nil {
		ready <- err
		return
	}
	ready <- nil

	for {
		select {
		case req := <-d.reqCh:
			req.reply <- netnsDialReply{conn: dialEntry(d.mappings[req.id])}
		case <-d.doneCh:
			return
		}
	}
}

// dialEntry dials the container-side target for one mapping. Runs on the
// setns'd worker thread. Container protocol is taken from m.Cont.Proto,
// which the parser fills — either explicitly from a tcp://port/udp://port
// override on the container endpoint, or implicitly from m.Scheme when no
// override is given. This is what enables cross-protocol mappings.
func dialEntry(m PortMapping) net.Conn {
	if m.Cont.Kind == KindNet {
		proto := m.Cont.Proto
		if proto == "" {
			proto = "tcp"
		}
		c, err := net.Dial(proto, fmt.Sprintf("127.0.0.1:%d", m.Cont.Port))
		if err != nil {
			return errConn{err: err}
		}
		return c
	}
	network := "unix"
	if m.Scheme == SchemeUDP {
		network = "unixgram"
	}
	c, err := net.Dial(network, m.Cont.Path)
	if err != nil {
		return errConn{err: err}
	}
	return c
}

// DialMapping implements Transport.
func (d *NetnsDialer) DialMapping(ctx context.Context, id int) (net.Conn, error) {
	reply := make(chan netnsDialReply, 1)
	select {
	case d.reqCh <- netnsDialReq{id: id, reply: reply}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-d.doneCh:
		return nil, errors.New("netns dialer closed")
	}
	select {
	case r := <-reply:
		if ec, ok := r.conn.(errConn); ok {
			return nil, ec.err
		}
		return r.conn, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close stops the worker goroutine. The pinned OS thread is then destroyed
// by the Go runtime instead of reused.
func (d *NetnsDialer) Close() error {
	select {
	case <-d.doneCh:
		// already closed
	default:
		close(d.doneCh)
	}
	return nil
}

// errConn carries a dial error through the channel so we don't need a
// separate reply field. It only exists inside dialEntry/DialMapping.
type errConn struct {
	net.Conn
	err error
}
