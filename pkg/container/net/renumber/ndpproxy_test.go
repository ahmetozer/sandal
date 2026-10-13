//go:build linux

package renumber

import (
	"net"
	"testing"
)

// The uplink may not exist yet at daemon start (created later by a
// --startup container). The proxy must construct anyway and stay quiet.
func TestNDPProxyToleratesMissingUpstream(t *testing.T) {
	p := NewNDPProxy("sandaltest-missing0", "sandaltest-missing1")
	if p == nil {
		t.Fatal("NewNDPProxy returned nil")
	}
	p.Reconcile([]net.IP{net.ParseIP("2001:db8::1")}) // must not panic
}
