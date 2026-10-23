//go:build linux

package build

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/diskimage"
	"github.com/ahmetozer/sandal/pkg/env"
	libbuild "github.com/ahmetozer/sandal/pkg/lib/container/build"
	"github.com/ahmetozer/sandal/pkg/lib/loopdev"
	"golang.org/x/sys/unix"
)

// useTempSandalDirs points every sandal directory the builder touches at a
// fresh temp tree and makes sure no daemon socket is reachable, so the test
// never writes into (or talks to) the host's real sandal state.
func useTempSandalDirs(t *testing.T) string {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root (loop devices, squashfs and overlay mounts)")
	}
	if isVM, _ := env.IsVM(); isVM {
		t.Skip("not supported inside a sandal VM")
	}
	root := t.TempDir()
	vars := map[*string]string{
		&env.LibDir:                root,
		&env.RunDir:                root,
		&env.BaseImageDir:          filepath.Join(root, "image"),
		&env.BaseStateDir:          filepath.Join(root, "state"),
		&env.BaseChangeDir:         filepath.Join(root, "changedir"),
		&env.BaseTempDir:           filepath.Join(root, "tmp"),
		&env.BaseRootfsDir:         filepath.Join(root, "rootfs"),
		&env.BaseImmutableImageDir: filepath.Join(root, "immutable"),
		&env.DaemonSocket:          filepath.Join(root, "no-daemon.sock"),
	}
	for p, v := range vars {
		old := *p
		*p = v
		t.Cleanup(func() { *p = old })
		if err := os.MkdirAll(v, 0o755); err != nil && p != &env.DaemonSocket {
			t.Fatal(err)
		}
	}
	// Never leave a squashfs mounted on the shared test host, even when the
	// test fails half-way; TempDir cleanup could not remove it otherwise.
	t.Cleanup(func() {
		for _, mp := range immutableMounts(t) {
			// The mount dir is named after its loop number.
			no, err := strconv.Atoi(filepath.Base(mp))
			if err != nil {
				_ = unix.Unmount(mp, unix.MNT_DETACH)
				continue
			}
			img := diskimage.ImmutableImage{MountDir: mp,
				LoopConfig: loopdev.Config{No: no, Path: loopdev.DevicePath(no)}}
			_ = diskimage.Umount(&img)
		}
	})
	return root
}

// newRequest parses a Dockerfile and returns a BuildRequest over ctxDir.
func newRequest(t *testing.T, dockerfile, ctxDir, tag string) BuildRequest {
	t.Helper()
	instrs, err := libbuild.ParseDockerfile(strings.NewReader(dockerfile))
	if err != nil {
		t.Fatal(err)
	}
	globalArgs, stages, err := libbuild.SplitStages(instrs)
	if err != nil {
		t.Fatal(err)
	}
	return BuildRequest{
		GlobalArgs: globalArgs,
		Stages:     stages,
		ContextDir: ctxDir,
		Tag:        tag,
		buildID:    "test",
	}
}

// immutableMounts lists the mount points under env.BaseImmutableImageDir.
func immutableMounts(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) > 1 && strings.HasPrefix(f[1], env.BaseImmutableImageDir+"/") {
			out = append(out, f[1])
		}
	}
	return out
}

// TestRunCopyFromStage builds a two-stage image where the final stage
// copies a file out of the first one. The first stage's rootfs is only
// available as its squashfs once the stage is finished, so this covers
// COPY --from=<stage> reading from that image.
func TestRunCopyFromStage(t *testing.T) {
	useTempSandalDirs(t)
	ctxDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(ctxDir, "msg"), []byte("hello from stage0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := newRequest(t, `FROM scratch AS build
COPY msg /out/msg
FROM scratch
COPY --from=build /out/msg /msg
`, ctxDir, "bd-copyfrom:test")

	out, err := Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if left := immutableMounts(t); len(left) > 0 {
		t.Errorf("image mounts left after the build: %v", left)
	}

	img, err := diskimage.Mount(out)
	if err != nil {
		t.Fatalf("mount output %s: %v", out, err)
	}
	defer diskimage.Umount(&img)
	got, err := os.ReadFile(filepath.Join(img.MountDir, "msg"))
	if err != nil {
		t.Fatalf("final image is missing /msg: %v", err)
	}
	if string(got) != "hello from stage0\n" {
		t.Fatalf("/msg = %q, want %q", got, "hello from stage0\n")
	}
}
