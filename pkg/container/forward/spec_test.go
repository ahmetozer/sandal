package forward

import "testing"

func TestParseFlag(t *testing.T) {
	cases := []struct {
		in      string
		scheme  Scheme
		hostIP  string
		hostPt  int
		hostPath string
		contIP  string
		contPt  int
		contPath string
		wantErr bool
	}{
		{in: "443", scheme: SchemeTCP, hostIP: "127.0.0.1", hostPt: 443, contIP: "127.0.0.1", contPt: 443},
		{in: "0.0.0.0:443", scheme: SchemeTCP, hostIP: "0.0.0.0", hostPt: 443, contIP: "127.0.0.1", contPt: 443},
		{in: "0.0.0.0:443:8443", scheme: SchemeTCP, hostIP: "0.0.0.0", hostPt: 443, contIP: "127.0.0.1", contPt: 8443},
		{in: "0.0.0.0:443:unix:///tmp/l.sock", scheme: SchemeTCP, hostIP: "0.0.0.0", hostPt: 443, contPath: "/tmp/l.sock"},
		{in: "udp://0.0.0.0:53:unix:///tmp/u.sock", scheme: SchemeUDP, hostIP: "0.0.0.0", hostPt: 53, contPath: "/tmp/u.sock"},
		{in: "tls://0.0.0.0:443:8080", scheme: SchemeTLS, hostIP: "0.0.0.0", hostPt: 443, contIP: "127.0.0.1", contPt: 8080},
		{in: "tls://0.0.0.0:443:unix:///tmp/l.sock", scheme: SchemeTLS, hostIP: "0.0.0.0", hostPt: 443, contPath: "/tmp/l.sock"},
		{in: "unix:///run/host.sock:8080", scheme: SchemeTCP, hostPath: "/run/host.sock", contIP: "127.0.0.1", contPt: 8080},
		{in: "unix:///run/host.sock:unix:///run/c.sock", scheme: SchemeTCP, hostPath: "/run/host.sock", contPath: "/run/c.sock"},
		{in: "udp://unix:///run/h.sock:53", scheme: SchemeUDP, hostPath: "/run/h.sock", contIP: "127.0.0.1", contPt: 53},
		{in: "unix:///run/host.sock", wantErr: true}, // container endpoint required
		{in: "bogus://1", wantErr: true},
		{in: "", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseFlag(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Scheme != tc.scheme {
				t.Errorf("scheme: got %q want %q", got.Scheme, tc.scheme)
			}
			if tc.hostPath != "" {
				if got.Host.Kind != KindUnix || got.Host.Path != tc.hostPath {
					t.Errorf("host unix: got %+v want %q", got.Host, tc.hostPath)
				}
			} else {
				if got.Host.Kind != KindNet || got.Host.IP != tc.hostIP || got.Host.Port != tc.hostPt {
					t.Errorf("host net: got %+v want %s:%d", got.Host, tc.hostIP, tc.hostPt)
				}
			}
			if tc.contPath != "" {
				if got.Cont.Kind != KindUnix || got.Cont.Path != tc.contPath {
					t.Errorf("cont unix: got %+v want %q", got.Cont, tc.contPath)
				}
			} else {
				if got.Cont.Kind != KindNet || got.Cont.IP != tc.contIP || got.Cont.Port != tc.contPt {
					t.Errorf("cont net: got %+v want %s:%d", got.Cont, tc.contIP, tc.contPt)
				}
			}
		})
	}
}
