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
// It pins a single goroutine to a dedicated OS thread, setns-es that thread
// into the target container's network and mount namespaces, and then serves
// DialMapping requests over a channel. Because only socket(2)/connect(2)
// consult the calling thread's namespaces, subsequent read/write on the
// returned net.Conn runs on any thread without issue.
//
// Lifecycle: the worker goroutine runs until Close() is called. It never
// calls UnlockOSThread, so when the goroutine returns the Go runtime
// destroys the (now-tainted) thread instead of reusing it.
type NetnsDialer struct {
	mappings []PortMapping
	reqCh    chan netnsDialReq
	doneCh   chan struct{}
}

type netnsDialReq struct {
	id    int
	reply chan netnsDialReply
}

type netnsDialReply struct {
	conn net.Conn
	err  error
}

// StartNetnsDialer creates a NetnsDialer that can reach the container whose
// init process is at contPid. Blocks until the worker has successfully
// entered the container namespaces or failed to do so.
func StartNetnsDialer(contPid int, mappings []PortMapping) (*NetnsDialer, error) {
	d := &NetnsDialer{
		mappings: mappings,
		reqCh:    make(chan netnsDialReq),
		doneCh:   make(chan struct{}),
	}
	ready := make(chan error, 1)
	go d.worker(contPid, ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return d, nil
}

// worker runs on a dedicated OS thread that is setns'd into the target
// container. All namespace-sensitive syscalls (net.Dial in particular) are
// serialized through this goroutine.
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
// setns'd worker thread.
func dialEntry(m PortMapping) net.Conn {
	if m.Cont.Kind == KindNet {
		proto := "tcp"
		if m.Scheme == SchemeUDP {
			proto = "udp"
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
