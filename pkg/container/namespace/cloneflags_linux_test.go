//go:build linux

package namespace

import (
	"syscall"
	"testing"
)

// TestCloneflagsExcludesUserDefined verifies that Cloneflags() does NOT set
// the CLONE_NEW* bit for entries marked IsUserDefined. CLONE_NEW* means
// "create a new namespace" — when the user is joining an existing one we
// must not also create a new one.
func TestCloneflagsExcludesUserDefined(t *testing.T) {
	pidStr := "pid:1"
	hostStr := "host"
	ns := Namespaces{
		"net":  NamespaceConf{UserValue: &pidStr, IsUserDefined: true},
		"mnt":  NamespaceConf{UserValue: &pidStr, IsUserDefined: true},
		"user": NamespaceConf{UserValue: &hostStr, IsHost: true},
	}
	got := ns.Cloneflags()
	if got != 0 {
		t.Fatalf("Cloneflags with only IsUserDefined+IsHost entries = %#x, want 0", got)
	}
}

// TestCloneflagsIncludesCreateNew verifies the positive case: entries that
// are neither host nor user-defined (i.e. "create new") still produce the
// expected CLONE_NEW* bits.
func TestCloneflagsIncludesCreateNew(t *testing.T) {
	empty := ""
	ns := Namespaces{
		"net": NamespaceConf{UserValue: &empty}, // create new
		"mnt": NamespaceConf{UserValue: &empty}, // create new
	}
	got := ns.Cloneflags()
	want := uintptr(syscall.CLONE_NEWNET | syscall.CLONE_NEWNS)
	if got != want {
		t.Fatalf("Cloneflags(net+mnt create-new) = %#x, want %#x", got, want)
	}
}

// TestCloneflagsMixed verifies the realistic mix: some create-new, some
// host, some user-defined. Only the create-new entries contribute bits.
func TestCloneflagsMixed(t *testing.T) {
	empty := ""
	host := "host"
	target := "pid:1234"
	ns := Namespaces{
		"net":    NamespaceConf{UserValue: &target, IsUserDefined: true}, // join, no bit
		"mnt":    NamespaceConf{UserValue: &empty},                       // create new, bit
		"user":   NamespaceConf{UserValue: &host, IsHost: true},          // host, no bit
		"uts":    NamespaceConf{UserValue: &empty},                       // create new, bit
		"ipc":    NamespaceConf{UserValue: &host, IsHost: true},          // host, no bit
		"cgroup": NamespaceConf{UserValue: &target, IsUserDefined: true}, // join, no bit
		"pid":    NamespaceConf{UserValue: &empty},                       // create new, bit
	}
	got := ns.Cloneflags()
	want := uintptr(syscall.CLONE_NEWNS | syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID)
	if got != want {
		t.Fatalf("Cloneflags(mixed) = %#x, want %#x", got, want)
	}
}
