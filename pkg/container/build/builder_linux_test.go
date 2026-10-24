//go:build linux

package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/diskimage"
	"github.com/ahmetozer/sandal/pkg/container/overlayfs"
	"github.com/ahmetozer/sandal/pkg/env"
	libbuild "github.com/ahmetozer/sandal/pkg/lib/container/build"
	"github.com/ahmetozer/sandal/pkg/lib/container/registry"
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

// stageImages lists the per-stage squashfs files left in env.BaseImageDir.
func stageImages(t *testing.T) []string {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(env.BaseImageDir, ".sandal-build-*-stage*.sqfs"))
	if err != nil {
		t.Fatal(err)
	}
	return left
}

// TestRunRemovesStageImages checks that the intermediate per-stage
// squashfs files are removed after a build, whether it succeeds or fails.
func TestRunRemovesStageImages(t *testing.T) {
	for name, tc := range map[string]struct {
		dockerfile string
		wantErr    bool
	}{
		"success": {dockerfile: `FROM scratch AS build
COPY msg /out/msg
FROM scratch
COPY --from=build /out/msg /msg
`},
		"failure": {dockerfile: `FROM scratch AS build
COPY msg /out/msg
FROM scratch
COPY --from=build /out/missing /msg
`, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			useTempSandalDirs(t)
			ctxDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(ctxDir, "msg"), []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Run(context.Background(), newRequest(t, tc.dockerfile, ctxDir, "bd-stages:test"))
			if (err != nil) != tc.wantErr {
				t.Fatalf("Run error = %v, wantErr %v", err, tc.wantErr)
			}
			if left := stageImages(t); len(left) > 0 {
				t.Errorf("stage images left after the build: %v", left)
			}
		})
	}
}

func TestBuildChangeDirSize(t *testing.T) {
	for _, tc := range []struct {
		size        string
		imageBacked bool
		want        string
	}{
		{"", true, defaultBuildChangeDirSize}, // VM or overlay host: documented 8g
		{"", false, ""},                       // folder mode: no image, no size
		{"4g", true, "4g"},                    // -csize always wins
		{"4g", false, "4g"},
	} {
		if got := buildChangeDirSize(tc.size, tc.imageBacked); got != tc.want {
			t.Errorf("buildChangeDirSize(%q, %v) = %q, want %q", tc.size, tc.imageBacked, got, tc.want)
		}
	}
	if defaultBuildChangeDirSize != "8g" {
		t.Errorf("defaultBuildChangeDirSize = %q, want 8g (docs/commands/build.md)", defaultBuildChangeDirSize)
	}
}

// Inside a VM the -csize temp dir is backed by an ext4 image on the virtiofs
// share of the host's sandal lib dir, which every build VM sees. Each build
// must get its own image, so concurrent builds do not share one.
func TestVMTempImagePerBuild(t *testing.T) {
	const tempDir = "/var/lib/sandal/tmp"
	a, b := vmTempImage(tempDir, "0a1b2c3d"), vmTempImage(tempDir, "4e5f6a7b")
	if a == b {
		t.Fatalf("builds share the temp image %s", a)
	}
	for _, img := range []string{a, b} {
		if img == tempDir+".img" {
			t.Errorf("temp image %s is the shared legacy name", img)
		}
		if filepath.Dir(img) != filepath.Dir(tempDir) || !strings.HasSuffix(img, ".img") {
			t.Errorf("temp image %s is not an .img next to %s", img, tempDir)
		}
	}
}

// The temp dir image is removed when the build ends, and two builds over
// the same temp dir never touch each other's image.
func TestPrepareVMTempDirCleanup(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root (loop devices and ext4 mounts)")
	}
	tempDir := filepath.Join(t.TempDir(), "tmp")

	cleanupA, err := prepareVMTempDir(tempDir, "0a1b2c3d", "64m")
	if err != nil {
		t.Fatal(err)
	}
	cleanupB, err := prepareVMTempDir(tempDir, "4e5f6a7b", "64m")
	if err != nil {
		cleanupA()
		t.Fatal(err)
	}
	imgA, imgB := vmTempImage(tempDir, "0a1b2c3d"), vmTempImage(tempDir, "4e5f6a7b")
	for _, img := range []string{imgA, imgB} {
		if _, err := os.Stat(img); err != nil {
			t.Errorf("temp image of a running build: %v", err)
		}
	}

	cleanupB()
	if _, err := os.Stat(imgB); !os.IsNotExist(err) {
		t.Errorf("image %s left after its build ended (stat err %v)", imgB, err)
	}
	if _, err := os.Stat(imgA); err != nil {
		t.Errorf("ending one build removed the other's image: %v", err)
	}
	cleanupA()
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(tempDir), "*.img")); len(left) != 0 {
		t.Errorf("images left after both builds ended: %v", left)
	}
	if isMountPoint(t, tempDir) {
		t.Errorf("%s still mounted after both builds ended", tempDir)
	}
}

// A setup that fails after creating the image leaves no image behind.
func TestPrepareVMTempDirFailureLeavesNothing(t *testing.T) {
	tempDir := filepath.Join(t.TempDir(), "tmp")
	prev := prepareImageChangeDir
	prepareImageChangeDir = func(changeDir, imagePath, size string) (*overlayfs.ImageChangeMount, error) {
		if err := os.WriteFile(imagePath, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		return nil, errors.New("mounting ext4: invalid argument")
	}
	t.Cleanup(func() { prepareImageChangeDir = prev })

	if _, err := prepareVMTempDir(tempDir, "0a1b2c3d", "64m"); err == nil {
		t.Fatal("prepareVMTempDir succeeded although the mount failed")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(tempDir), "*.img")); len(left) != 0 {
		t.Errorf("images left after a failed setup: %v", left)
	}
}

func isMountPoint(t *testing.T, dir string) bool {
	t.Helper()
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) > 4 && f[4] == dir {
			return true
		}
	}
	return false
}

// stubPublish replaces the push and its source mount for publishImage. push
// runs in place of libbuild.Push; the returned pointer receives the mount
// state of the source at push time.
func stubPublish(t *testing.T, mountDir string, push func(libbuild.PushOpts) error) *bool {
	t.Helper()
	released := new(bool)
	prevMount, prevPush := mountPushSource, pushImage
	mountPushSource = func(sqfs string) (string, func(), error) {
		return mountDir, func() { *released = true }, nil
	}
	pushImage = func(ctx context.Context, opts libbuild.PushOpts) error { return push(opts) }
	t.Cleanup(func() { mountPushSource, pushImage = prevMount, prevPush })
	return released
}

// publishFixture writes an old tagged image with its sidecar and a new
// stage image, and returns their paths.
func publishFixture(t *testing.T) (finalSqfs, outPath string) {
	t.Helper()
	dir := t.TempDir()
	finalSqfs = filepath.Join(dir, ".sandal-build-test-stage0.sqfs")
	outPath = filepath.Join(dir, "bd-publish_test.sqfs")
	for path, data := range map[string]string{finalSqfs: "new", outPath: "old", outPath + ".json": `{"old":true}`} {
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return finalSqfs, outPath
}

// A VM build's host only sees whether outPath became a new file, so a push
// that fails must leave the previous image and its sidecar untouched.
func TestPublishImagePushFailureKeepsPreviousImage(t *testing.T) {
	finalSqfs, outPath := publishFixture(t)
	before, err := os.Stat(outPath)
	if err != nil {
		t.Fatal(err)
	}
	stubPublish(t, t.TempDir(), func(libbuild.PushOpts) error { return errors.New("registry unreachable") })

	req := &BuildRequest{Tag: "bd-publish:test", Push: true}
	target := &libbuild.Stage{Config: registryConfig("NEW=1")}
	if err := publishImage(context.Background(), req, target, finalSqfs, outPath); err == nil {
		t.Fatal("publishImage succeeded although the push failed")
	}
	after, err := os.Stat(outPath)
	if err != nil || !os.SameFile(before, after) {
		t.Errorf("a failed push replaced %s (stat err %v)", outPath, err)
	}
	if got, _ := os.ReadFile(outPath + ".json"); string(got) != `{"old":true}` {
		t.Errorf("a failed push rewrote the sidecar: %s", got)
	}
	if _, err := os.Stat(finalSqfs + ".json"); !os.IsNotExist(err) {
		t.Errorf("staged sidecar left after a failed push (stat err %v)", err)
	}
}

// The push reads the final stage image through a mount of it, and the
// rename to outPath happens only after the push.
func TestPublishImagePushesFromStageImage(t *testing.T) {
	finalSqfs, outPath := publishFixture(t)
	mountDir := t.TempDir()
	var pushed libbuild.PushOpts
	var outAtPush string
	released := stubPublish(t, mountDir, func(opts libbuild.PushOpts) error {
		pushed = opts
		data, _ := os.ReadFile(outPath)
		outAtPush = string(data)
		return nil
	})

	req := &BuildRequest{Tag: "bd-publish:test", Push: true}
	target := &libbuild.Stage{Config: registryConfig("NEW=1")}
	if err := publishImage(context.Background(), req, target, finalSqfs, outPath); err != nil {
		t.Fatalf("publishImage: %v", err)
	}
	if pushed.RootfsDir != mountDir {
		t.Errorf("pushed from %q, want the stage image mount %q", pushed.RootfsDir, mountDir)
	}
	if pushed.Tag != req.Tag {
		t.Errorf("pushed tag %q, want %q", pushed.Tag, req.Tag)
	}
	if !*released {
		t.Error("stage image mount not released after the push")
	}
	if outAtPush != "old" {
		t.Errorf("%s held %q during the push, want the previous image", outPath, outAtPush)
	}
	if got, _ := os.ReadFile(outPath); string(got) != "new" {
		t.Errorf("%s = %q after publish, want the new image", outPath, got)
	}
	if got, _ := os.ReadFile(outPath + ".json"); !strings.Contains(string(got), "NEW=1") {
		t.Errorf("sidecar not updated: %s", got)
	}
	if _, err := os.Stat(finalSqfs); !os.IsNotExist(err) {
		t.Errorf("stage image still at %s (stat err %v)", finalSqfs, err)
	}
}

// Without --push nothing is mounted or pushed.
func TestPublishImageWithoutPush(t *testing.T) {
	finalSqfs, outPath := publishFixture(t)
	prevMount := mountPushSource
	mountPushSource = func(string) (string, func(), error) {
		t.Error("stage image mounted without --push")
		return "", func() {}, nil
	}
	t.Cleanup(func() { mountPushSource = prevMount })
	stubPushNever := pushImage
	pushImage = func(context.Context, libbuild.PushOpts) error { t.Error("pushed without --push"); return nil }
	t.Cleanup(func() { pushImage = stubPushNever })

	req := &BuildRequest{Tag: "bd-publish:test"}
	if err := publishImage(context.Background(), req, &libbuild.Stage{}, finalSqfs, outPath); err != nil {
		t.Fatalf("publishImage: %v", err)
	}
	if got, _ := os.ReadFile(outPath); string(got) != "new" {
		t.Errorf("%s = %q, want the new image", outPath, got)
	}
}

func registryConfig(env ...string) registry.RuntimeConfig {
	return registry.RuntimeConfig{Env: env}
}
