//go:build linux

package host

import (
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/namespace"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"golang.org/x/sys/unix"
)

// nsOf builds a defaulted namespace set from -ns-<kind> values; kinds not
// in vals get the CLI defaults (user: host, the rest: new).
func nsOf(t *testing.T, vals map[namespace.Name]string) namespace.Namespaces {
	t.Helper()
	ns := namespace.Namespaces{}
	for _, kind := range []namespace.Name{"mnt", "ipc", "cgroup", "pid", "net", "user", "uts"} {
		v, ok := vals[kind]
		if !ok && kind == "user" {
			v = "host"
		}
		ns[kind] = namespace.NamespaceConf{UserValue: &v}
	}
	if err := ns.Defaults(); err != nil {
		t.Fatal(err)
	}
	return ns
}

func TestMapUserNS(t *testing.T) {
	full := []syscall.SysProcIDMap{{ContainerID: 0, HostID: 0, Size: crt.ProcSize}}
	cases := []struct {
		name string
		vals map[namespace.Name]string
		want bool
	}{
		{"new user ns", map[namespace.Name]string{"user": ""}, true},
		{"new user ns, host pid ns", map[namespace.Name]string{"user": "", "pid": "host"}, true},
		{"host user ns (default)", nil, false},
		{"joined user ns", map[namespace.Name]string{"user": "pid:1"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := nsOf(t, tc.vals)
			attr := &syscall.SysProcAttr{Cloneflags: ns.Cloneflags()}
			got := mapUserNS(attr, ns)
			if got != tc.want {
				t.Fatalf("mapUserNS = %v, want %v", got, tc.want)
			}
			// Maps belong exactly to a clone that creates the user ns: Go
			// writes them to the child's uid_map/gid_map, which fails for a
			// child still in the caller's user namespace.
			if creates := attr.Cloneflags&syscall.CLONE_NEWUSER != 0; creates != tc.want {
				t.Fatalf("CLONE_NEWUSER set = %v, mapUserNS = %v", creates, got)
			}
			if !tc.want {
				if attr.UidMappings != nil || attr.GidMappings != nil {
					t.Fatalf("maps set without a new user ns: uid %v gid %v", attr.UidMappings, attr.GidMappings)
				}
				return
			}
			if len(attr.UidMappings) != 1 || attr.UidMappings[0] != full[0] {
				t.Fatalf("UidMappings = %v, want %v", attr.UidMappings, full)
			}
			if len(attr.GidMappings) != 1 || attr.GidMappings[0] != full[0] {
				t.Fatalf("GidMappings = %v, want %v", attr.GidMappings, full)
			}
			if !attr.GidMappingsEnableSetgroups {
				t.Fatal("setgroups denied in the container: su, sudo and apt fail")
			}
		})
	}
}

const userNSHelperEnv = "SANDAL_TEST_USERNS_HELPER"

// TestUserNSHelper is not a test: TestMapUserNSChild runs it in the child
// it clones into a new user namespace.
func TestUserNSHelper(t *testing.T) {
	if os.Getenv(userNSHelperEnv) == "" {
		return
	}
	// The first privileged step of container init (guest.ContainerInitProc).
	if err := unix.Sethostname([]byte("t8-userns")); err != nil {
		t.Fatalf("sethostname: %v", err)
	}
	// What su, sudo and apt do when they drop privileges.
	if err := unix.Setgroups([]int{0}); err != nil {
		t.Fatalf("setgroups: %v", err)
	}
}

// TestMapUserNSChild clones a child the way crun does for -ns-user "" and
// checks it is root with capabilities in its new user namespace.
func TestMapUserNSChild(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("e2e: requires root")
	}
	// Only user and uts are new: the child's hostname change stays private.
	host := map[namespace.Name]string{"user": "", "uts": ""}
	for _, kind := range []namespace.Name{"mnt", "ipc", "cgroup", "pid", "net"} {
		host[kind] = "host"
	}
	ns := nsOf(t, host)

	cmd := exec.Command(os.Args[0], "-test.run=^TestUserNSHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), userNSHelperEnv+"=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: ns.Cloneflags()}
	if cmd.SysProcAttr.Cloneflags != syscall.CLONE_NEWUSER|syscall.CLONE_NEWUTS {
		t.Fatalf("Cloneflags = %#x, want CLONE_NEWUSER|CLONE_NEWUTS", cmd.SysProcAttr.Cloneflags)
	}
	mapUserNS(cmd.SysProcAttr, ns)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child in a new user namespace: %v\n%s", err, out)
	}
}
