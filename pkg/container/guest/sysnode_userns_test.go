//go:build linux

package guest

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/namespace"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"golang.org/x/sys/unix"
)

const sysNodesHelperEnv = "SANDAL_TEST_SYSNODES_ROOT"

// TestSysNodesHelper is not a test: TestChildSysNodesInUserNS runs it in a
// child cloned into a new user and mount namespace, as container init is
// for -ns-user "".
func TestSysNodesHelper(t *testing.T) {
	root := os.Getenv(sysNodesHelperEnv)
	if root == "" {
		return
	}
	// Nothing below may propagate back to the host.
	if err := unix.Mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, ""); err != nil {
		t.Fatalf("make / private: %v", err)
	}
	// The layout childSysNodes runs in: the host root at /.old_root and a
	// fresh tmpfs /dev with devpts on /dev/pts (see childSysMounts).
	if err := unix.Mount("tmpfs", root, "tmpfs", 0, ""); err != nil {
		t.Fatalf("mount root tmpfs: %v", err)
	}
	for _, d := range []string{".old_root", "dev"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.Mount("/", filepath.Join(root, ".old_root"), "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
		t.Fatalf("bind host root: %v", err)
	}
	if err := unix.Mount("tmpfs", filepath.Join(root, "dev"), "tmpfs", unix.MS_RELATIME, "size=65536k,mode=755"); err != nil {
		t.Fatalf("mount /dev tmpfs: %v", err)
	}
	pts := filepath.Join(root, "dev", "pts")
	if err := os.Mkdir(pts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount("devpts", pts, "devpts", unix.MS_NOSUID|unix.MS_NOEXEC|unix.MS_RELATIME, "gid=5,mode=620,ptmxmode=666"); err != nil {
		t.Fatalf("mount devpts: %v", err)
	}
	if err := unix.Chroot(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("/"); err != nil {
		t.Fatal(err)
	}

	empty := ""
	ns := namespace.Namespaces{"user": {UserValue: &empty}}
	if err := ns.Defaults(); err != nil {
		t.Fatal(err)
	}
	if err := childSysNodes(&config.Config{NS: ns}); err != nil {
		t.Fatalf("childSysNodes: %v", err)
	}

	null, err := os.OpenFile("/dev/null", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open /dev/null: %v", err)
	}
	if _, err := null.Write([]byte("x")); err != nil {
		t.Fatalf("write /dev/null: %v", err)
	}
	null.Close()

	zero, err := os.Open("/dev/zero")
	if err != nil {
		t.Fatalf("open /dev/zero: %v", err)
	}
	b := []byte{1}
	if _, err := zero.Read(b); err != nil || b[0] != 0 {
		t.Fatalf("read /dev/zero: %v %v", b, err)
	}
	zero.Close()

	// A pty for `sandal exec -t` and for shells: /dev/ptmx must reach the
	// container's devpts.
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	ptmx.Close()
}

// TestChildSysNodesInUserNS checks container init's device nodes work in a
// user namespace, where mknod is not permitted and a tmpfs /dev is nodev.
func TestChildSysNodesInUserNS(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("e2e: requires root")
	}
	idmap := []syscall.SysProcIDMap{{ContainerID: 0, HostID: 0, Size: crt.ProcSize}}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSysNodesHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), sysNodesHelperEnv+"="+t.TempDir())
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:                 syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS,
		UidMappings:                idmap,
		GidMappings:                idmap,
		GidMappingsEnableSetgroups: true,
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("container init in a user namespace: %v\n%s", err, out)
	}
}
