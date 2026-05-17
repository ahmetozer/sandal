//go:build linux

package renumber

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestPublishDoesNotBlockWhenChanFull: the PD source's `publish` method
// runs synchronously inside Renew/Rebind via the lease loop. The outgoing
// channel `s.out` has capacity 1, so if a previous prefix is still parked
// in the buffer, the cap-1 channel is full and `publish` blocks — freezing
// the lease loop. The contract: publish must replace any pending value
// rather than block.
func TestPublishDoesNotBlockWhenChanFull(t *testing.T) {
	s := &PDSource{out: make(chan *net.IPNet, 1)}

	// Park a stale prefix in the buffer; nobody is reading.
	old, _ := newCIDR("2001:db8:1::/64")
	s.out <- old

	// Publish a new prefix. Must not block more than a few ms.
	done := make(chan error, 1)
	go func() {
		// publish() expects a "delegated" prefix from which it sub-allocates
		// a /64. Pass a /56 to exercise the sub-allocate path realistically.
		del, _ := newCIDR("2001:db8:2::/56")
		done <- s.publish(context.Background(), del)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("publish returned error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("publish blocked despite full channel")
	}

	// The channel now holds the new value; the old one was discarded.
	select {
	case got := <-s.out:
		want, _ := newCIDR("2001:db8:2::/64")
		if !cidrEqual(got, want) {
			t.Fatalf("expected %s in channel, got %s", want, got)
		}
	default:
		t.Fatal("expected new prefix in channel")
	}
}

func newCIDR(s string) (*net.IPNet, error) {
	_, n, err := net.ParseCIDR(s)
	return n, err
}

// TestPDSourceStopIsIdempotent: PDSource.Stop() can be invoked by both
// Service.Run()'s deferred Source.Stop and the lease loop's deferred
// Release on shutdown. Calling Stop twice must not crash, must not double-
// release. We can exercise the wrapper directly with no client/lease set:
// Stop should be a no-op in that state, and a second call also a no-op.
func TestPDSourceStopIsIdempotent(t *testing.T) {
	s := &PDSource{out: make(chan *net.IPNet, 1)}
	// No client, no lease: Stop is structurally allowed to do nothing.
	s.Stop()
	s.Stop()
	// If we reach here without panic or hang, the sync.Once guard works.
}
