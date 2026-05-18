//go:build linux

package namespace

import (
	"strings"
	"testing"
)

func TestValidateAllowsHostAndCreateNew(t *testing.T) {
	host := "host"
	empty := ""
	ns := Namespaces{
		"mnt":    NamespaceConf{UserValue: &empty},
		"pid":    NamespaceConf{UserValue: &empty},
		"net":    NamespaceConf{UserValue: &host, IsHost: true},
		"user":   NamespaceConf{UserValue: &host, IsHost: true},
		"ipc":    NamespaceConf{UserValue: &empty},
		"uts":    NamespaceConf{UserValue: &empty},
		"cgroup": NamespaceConf{UserValue: &empty},
	}
	if err := ns.Validate(); err != nil {
		t.Fatalf("Validate(default+host) = %v, want nil", err)
	}
}

func TestValidateAllowsUserDefinedNonMntNonPid(t *testing.T) {
	target := "pid:1234"
	ns := Namespaces{
		"net":    NamespaceConf{UserValue: &target, IsUserDefined: true},
		"uts":    NamespaceConf{UserValue: &target, IsUserDefined: true},
		"ipc":    NamespaceConf{UserValue: &target, IsUserDefined: true},
		"cgroup": NamespaceConf{UserValue: &target, IsUserDefined: true},
	}
	if err := ns.Validate(); err != nil {
		t.Fatalf("Validate(net+uts+ipc+cgroup joins) = %v, want nil", err)
	}
}

func TestValidateRejectsMntJoin(t *testing.T) {
	target := "file:/var/run/netns/x"
	ns := Namespaces{
		"mnt": NamespaceConf{UserValue: &target, IsUserDefined: true},
	}
	err := ns.Validate()
	if err == nil {
		t.Fatal("Validate(--ns-mnt file:...) = nil, want error")
	}
	if !strings.Contains(err.Error(), "--ns-mnt") {
		t.Fatalf("error %q does not mention --ns-mnt", err)
	}
}

func TestValidateRejectsPidJoin(t *testing.T) {
	target := "pid:1"
	ns := Namespaces{
		"pid": NamespaceConf{UserValue: &target, IsUserDefined: true},
	}
	err := ns.Validate()
	if err == nil {
		t.Fatal("Validate(--ns-pid pid:1) = nil, want error")
	}
	if !strings.Contains(err.Error(), "--ns-pid") {
		t.Fatalf("error %q does not mention --ns-pid", err)
	}
}

func TestValidateMntHostAllowed(t *testing.T) {
	host := "host"
	ns := Namespaces{
		"mnt": NamespaceConf{UserValue: &host, IsHost: true},
		"pid": NamespaceConf{UserValue: &host, IsHost: true},
	}
	if err := ns.Validate(); err != nil {
		t.Fatalf("Validate(mnt=host, pid=host) = %v, want nil", err)
	}
}

func TestValidateRejectsMalformedTarget(t *testing.T) {
	cases := []struct {
		name, value string
	}{
		{"garbage-prefix", "garbage:weird:input"},
		{"empty-after-pid", "pid:"},
		{"empty-after-file", "file:"},
		{"non-numeric-bare", "abc"},
		{"non-numeric-after-pid", "pid:abc"},
		{"negative-pid", "-1"},
		{"zero-pid", "0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.value
			ns := Namespaces{
				"net": NamespaceConf{UserValue: &v, IsUserDefined: true},
			}
			err := ns.Validate()
			if err == nil {
				t.Fatalf("Validate(--ns-net %q) = nil, want syntax error", tc.value)
			}
			if !strings.Contains(err.Error(), "--ns-net") {
				t.Fatalf("error %q does not mention --ns-net", err)
			}
		})
	}
}

func TestValidateAcceptsAllThreeTargetForms(t *testing.T) {
	cases := []string{
		"1",
		"12345",
		"pid:1",
		"pid:99999",
		"file:/var/run/netns/foo",
		"file:/some/other/path",
	}
	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			v := tc
			ns := Namespaces{
				"net": NamespaceConf{UserValue: &v, IsUserDefined: true},
			}
			if err := ns.Validate(); err != nil {
				t.Fatalf("Validate(--ns-net %q) = %v, want nil", tc, err)
			}
		})
	}
}
