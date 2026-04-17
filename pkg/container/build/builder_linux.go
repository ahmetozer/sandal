//go:build linux

package build

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/diskimage"
	"github.com/ahmetozer/sandal/pkg/env"
	libbuild "github.com/ahmetozer/sandal/pkg/lib/container/build"
	containerimage "github.com/ahmetozer/sandal/pkg/lib/container/image"
	"github.com/ahmetozer/sandal/pkg/lib/container/registry"
	"github.com/ahmetozer/sandal/pkg/lib/progress"
)

// BuildRequest is the runtime input for a build.
type BuildRequest struct {
	GlobalArgs []libbuild.Instruction // pre-FROM ARGs
	Stages     []*libbuild.Stage
	BuildArgs  map[string]string // --build-arg
	ContextDir string
	Tag        string
	Target     string // --target stage name; "" means last
	Push       bool
	Platform   registry.Platform

	// Backing selection (see BuildOpts for semantics). Forwarded to
	// the stage container's ChangeDir; sandal run's overlayfs code
	// handles the rest.
	TmpSize       uint
	ChangeDirSize string

	buildID     string            // generated; used for unique container names
	stageSqfs   map[int]string    // stageIdx → output .sqfs path
	containers  []*StageContainer // live stage containers, cleaned up at end
}

// Run executes the build and returns the local sqfs cache path of the
// final image. If req.Push is true, the image is also pushed to the
// registry implied by req.Tag.
//
// Each stage runs inside a real sandal container (one per FROM), so
// containerization, networking, and rootfs setup follow exactly the
// same code path `sandal run` uses. RUN steps re-enter the container
// via sandal.RunContainer; the overlay upper accumulates changes.
func Run(ctx context.Context, req BuildRequest) (string, error) {
	if len(req.Stages) == 0 {
		return "", fmt.Errorf("no stages")
	}
	if req.buildID == "" {
		req.buildID = genBuildID()
	}
	req.stageSqfs = map[int]string{}

	// Inside a VM, configure eth0 at the VM-host level so the FROM
	// pull (which runs in this process, OUTSIDE any container) has
	// network access. No-op outside a VM.
	if err := EnsureGuestNet(ctx); err != nil {
		return "", fmt.Errorf("setting up VM guest network: %w", err)
	}

	// Open build context (loads .dockerignore).
	bc, err := libbuild.NewBuildContext(req.ContextDir)
	if err != nil {
		return "", err
	}

	// Build-arg scope: --build-arg overrides ARG defaults declared in
	// the Dockerfile (we let Dockerfile ARGs be the keys; --build-arg
	// supplies the value).
	argScope := map[string]string{}
	for _, a := range req.GlobalArgs {
		applyArg(argScope, a)
	}
	for k, v := range req.BuildArgs {
		argScope[k] = v
	}

	// Determine target index.
	targetIdx := len(req.Stages) - 1
	if req.Target != "" {
		found := -1
		for i, s := range req.Stages {
			if s.Name == req.Target {
				found = i
				break
			}
		}
		if found < 0 {
			return "", fmt.Errorf("--target %q: stage not found", req.Target)
		}
		targetIdx = found
	}

	// Build each stage up to targetIdx (inclusive).
	for i := 0; i <= targetIdx; i++ {
		s := req.Stages[i]
		if err := buildStage(ctx, s, &req, bc, argScope); err != nil {
			cleanupAll(&req)
			return "", fmt.Errorf("stage %d: %w", i, err)
		}
	}

	target := req.Stages[targetIdx]
	finalSqfs := req.stageSqfs[targetIdx]
	if finalSqfs == "" {
		cleanupAll(&req)
		return "", fmt.Errorf("stage %d: no output image produced", targetIdx)
	}

	// Move (or link) the target stage's squashfs to the cache path
	// under the image tag.
	outPath := filepath.Join(env.BaseImageDir, containerimage.SanitizeRef(req.Tag)+".sqfs")
	if err := os.MkdirAll(env.BaseImageDir, 0755); err != nil {
		cleanupAll(&req)
		return "", err
	}
	if finalSqfs != outPath {
		_ = os.Remove(outPath)
		if err := os.Rename(finalSqfs, outPath); err != nil {
			cleanupAll(&req)
			return "", fmt.Errorf("finalising %s: %w", outPath, err)
		}
		req.stageSqfs[targetIdx] = outPath
	}

	// Sidecar JSON for runtime config.
	if err := writeConfigSidecar(outPath, target.Config); err != nil {
		cleanupAll(&req)
		return "", fmt.Errorf("writing config sidecar: %w", err)
	}

	// Push uses the live stage container's rootfs (still mounted) so
	// we can stream layers directly without re-extracting the squashfs.
	if req.Push {
		sc := findStageContainer(&req, targetIdx)
		if sc == nil {
			cleanupAll(&req)
			return "", fmt.Errorf("push: no live stage container for stage %d", targetIdx)
		}
		if err := libbuild.Push(ctx, libbuild.PushOpts{
			RootfsDir: sc.Cfg.RootfsDir,
			Tag:       req.Tag,
			Config:    target.Config,
			History:   target.History,
			Platform:  req.Platform,
		}); err != nil {
			cleanupAll(&req)
			return "", fmt.Errorf("push %s: %w", req.Tag, err)
		}
	}

	cleanupAll(&req)
	return outPath, nil
}

// cleanupAll tears down every live stage container. Safe to call
// multiple times; Cleanup is idempotent.
func cleanupAll(req *BuildRequest) {
	for _, sc := range req.containers {
		if sc != nil {
			sc.Cleanup()
		}
	}
	req.containers = nil
}

func findStageContainer(req *BuildRequest, idx int) *StageContainer {
	for _, sc := range req.containers {
		if sc != nil && sc.stageIdx == idx {
			return sc
		}
	}
	return nil
}

// buildStage materialises one stage: resolves its base squashfs,
// spins up a stage container backed by that base, applies all
// instructions via RunContainer, then snapshots the merged rootfs.
func buildStage(ctx context.Context, s *libbuild.Stage, req *BuildRequest, bc *libbuild.BuildContext, parentArgs map[string]string) error {
	// Stage-local arg scope inherits from the parent.
	argScope := map[string]string{}
	for k, v := range parentArgs {
		argScope[k] = v
	}

	// Expand BaseRef using current arg scope.
	baseRef := libbuild.Expand(s.BaseRef, argScope)
	if baseRef == "" {
		return fmt.Errorf("FROM: empty base reference after expansion")
	}

	baseSqfs, baseConfig, err := resolveBase(ctx, baseRef, req, s.Index)
	if err != nil {
		return fmt.Errorf("resolving base %s: %w", baseRef, err)
	}
	s.Config = baseConfig
	s.History = append(s.History, registry.History{
		Created:    nowRFC3339(),
		CreatedBy:  "FROM " + baseRef,
		EmptyLayer: true,
	})

	// Create (but do not start) the stage container. RUN steps and
	// the final snapshot all run against this container; sandal's
	// overlayfs code handles the stage rootfs mounting/tear-down.
	sc := NewStageContainer(s.Index, req.buildID, baseSqfs, req.TmpSize, req.ChangeDirSize)
	req.containers = append(req.containers, sc)
	s.RootfsDir = sc.Cfg.RootfsDir

	// Pre-mount the change-dir backing so COPY instructions placed
	// before the first RUN write into the persistent upper instead of
	// into a shadowed mount-point directory.
	if err := sc.PrepareUpper(); err != nil {
		return fmt.Errorf("preparing stage upper: %w", err)
	}

	// Apply instructions in order.
	for _, in := range s.Instrs {
		if err := applyInstruction(ctx, in, s, sc, bc, argScope, req); err != nil {
			return fmt.Errorf("line %d (%s): %w", in.Line, in.Kind, err)
		}
	}

	// Snapshot the merged rootfs to a per-stage squashfs. Last stage
	// is renamed to the final tag path in Run(); earlier stages land
	// in the image cache under a synthetic name so later FROMs can
	// reference them via the normal -lw pipeline.
	outSqfs := filepath.Join(env.BaseImageDir, fmt.Sprintf(".sandal-build-%s-stage%d.sqfs", req.buildID, s.Index))
	if err := sc.Finish(outSqfs); err != nil {
		return fmt.Errorf("snapshot stage: %w", err)
	}
	req.stageSqfs[s.Index] = outSqfs
	return nil
}

// resolveBase returns the squashfs path of the base for a stage, along
// with its runtime config. FROM scratch, a previous stage, or an OCI
// image reference.
func resolveBase(ctx context.Context, ref string, req *BuildRequest, currentIdx int) (string, registry.RuntimeConfig, error) {
	// FROM scratch — empty rootfs. We represent this as an empty
	// squashfs that sandal's mountRootfs can mount as Lower.
	if strings.EqualFold(ref, "scratch") {
		sqfs, err := emptyScratchSqfs()
		return sqfs, registry.RuntimeConfig{}, err
	}

	// FROM <previous-stage-name-or-index>
	for i := 0; i < currentIdx; i++ {
		prev := req.Stages[i]
		if prev.Name == ref || fmt.Sprintf("%d", prev.Index) == ref {
			sqfs := req.stageSqfs[prev.Index]
			if sqfs == "" {
				return "", registry.RuntimeConfig{}, fmt.Errorf("FROM %s: stage has no output (not yet built)", ref)
			}
			return sqfs, prev.Config, nil
		}
	}

	// FROM registry image.
	progressCh := make(chan progress.Event, 16)
	renderDone := progress.StartRenderer(progressCh, os.Stderr)
	sqfsPath, err := containerimage.Pull(ctx, ref, env.BaseImageDir, progressCh)
	close(progressCh)
	<-renderDone
	if err != nil {
		return "", registry.RuntimeConfig{}, err
	}
	// Inside a VM, env.BaseImageDir is virtiofs-backed and the
	// rename() from the .tmp file to the final cache path can take a
	// moment to propagate into the guest's own view (FUSE metadata
	// cache). Poll briefly so the immediate Lower mount that follows
	// sees the file.
	if err := waitForFile(sqfsPath, 5*time.Second); err != nil {
		return "", registry.RuntimeConfig{}, fmt.Errorf("waiting for pulled image to appear: %w", err)
	}
	cfg, _ := containerimage.LoadImageConfig(sqfsPath)
	var rt registry.RuntimeConfig
	if cfg != nil {
		rt = *cfg
	}
	return sqfsPath, rt, nil
}

// waitForFile polls for a file to become stat-able. Used after
// container-image Pull to bridge the virtiofs cache-propagation gap:
// the host sees the renamed file immediately, the guest sometimes
// takes a few hundred ms to catch up on FUSE metadata invalidation.
func waitForFile(path string, max time.Duration) error {
	deadline := time.Now().Add(max)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("stat %s: still missing after %s", path, max)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// emptyScratchSqfs creates (or returns cached) an empty squashfs image
// usable as a scratch Lower.
func emptyScratchSqfs() (string, error) {
	path := filepath.Join(env.BaseImageDir, ".sandal-build-scratch.sqfs")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(env.BaseImageDir, 0755); err != nil {
		return "", err
	}
	empty, err := os.MkdirTemp(env.BaseTempDir, "sandal-build-scratch-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(empty)
	if err := writeSquashfs(empty, path); err != nil {
		return "", err
	}
	return path, nil
}

// applyInstruction dispatches one parsed instruction to the appropriate
// handler. RUN steps execute against the stage container (which
// accumulates overlay state); COPY/ADD writes into the container's
// overlay upper directory; metadata-only instructions update
// s.Config for the final image-config sidecar.
func applyInstruction(ctx context.Context, in libbuild.Instruction, s *libbuild.Stage, sc *StageContainer, bc *libbuild.BuildContext, argScope map[string]string, req *BuildRequest) error {
	_ = ctx
	switch in.Kind {
	case libbuild.InstrEnv:
		applyEnv(s, in.Args, stageVars(s, argScope))
	case libbuild.InstrLabel:
		applyLabel(s, in.Args)
	case libbuild.InstrArg:
		applyArg(argScope, in)
	case libbuild.InstrWorkDir:
		if len(in.Args) > 0 {
			wd := libbuild.Expand(in.Args[0], stageVars(s, argScope))
			if !strings.HasPrefix(wd, "/") {
				wd = filepath.Join("/", s.Config.WorkingDir, wd)
			}
			s.Config.WorkingDir = wd
			// Materialise the directory in the overlay upper so RUN
			// steps see it.
			abs := filepath.Join(sc.WriteUpper(), wd)
			if err := os.MkdirAll(abs, 0755); err != nil {
				return fmt.Errorf("WORKDIR mkdir %s: %w", wd, err)
			}
		}
	case libbuild.InstrUser:
		if len(in.Args) > 0 {
			s.Config.User = libbuild.Expand(in.Args[0], stageVars(s, argScope))
		}
	case libbuild.InstrCmd:
		s.Config.Cmd = expandAll(in.Args, stageVars(s, argScope))
	case libbuild.InstrEntrypoint:
		s.Config.Entrypoint = expandAll(in.Args, stageVars(s, argScope))
	case libbuild.InstrExpose:
		applyExpose(s, expandAll(in.Args, stageVars(s, argScope)))
	case libbuild.InstrVolume:
		applyVolume(s, expandAll(in.Args, stageVars(s, argScope)))
	case libbuild.InstrStopSignal:
		if len(in.Args) > 0 {
			s.Config.StopSignal = in.Args[0]
		}
	case libbuild.InstrShell:
		// Shell-form RUN/CMD/ENTRYPOINT override — recorded elsewhere.
	case libbuild.InstrCopy, libbuild.InstrAdd:
		if err := applyCopy(in, s, sc, bc, argScope, req); err != nil {
			return err
		}
	case libbuild.InstrRun:
		args := instructionArgs(in, nil)
		if err := sc.ExecRun(args, s.Config.Env, s.Config.WorkingDir, s.Config.User); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported instruction %s", in.Kind)
	}

	// Record History entry.
	emptyLayer := true
	switch in.Kind {
	case libbuild.InstrCopy, libbuild.InstrAdd, libbuild.InstrRun:
		emptyLayer = false
	}
	s.History = append(s.History, registry.History{
		Created:    nowRFC3339(),
		CreatedBy:  in.Raw,
		EmptyLayer: emptyLayer,
	})
	return nil
}

func applyEnv(s *libbuild.Stage, kvs []string, vars map[string]string) {
	envMap := map[string]string{}
	for _, e := range s.Config.Env {
		k, v, _ := strings.Cut(e, "=")
		envMap[k] = v
	}
	expandScope := map[string]string{}
	for k, v := range vars {
		expandScope[k] = v
	}
	for k, v := range envMap {
		expandScope[k] = v
	}
	for _, kv := range kvs {
		k, v, _ := strings.Cut(kv, "=")
		expanded := libbuild.Expand(v, expandScope)
		envMap[k] = expanded
		expandScope[k] = expanded
	}
	s.Config.Env = mapToEnvList(envMap, kvs)
}

// mapToEnvList rebuilds the env slice preserving order with duplicates collapsed.
func mapToEnvList(envMap map[string]string, latest []string) []string {
	seen := map[string]bool{}
	var out []string
	latestKeys := map[string]bool{}
	for _, e := range latest {
		k, _, _ := strings.Cut(e, "=")
		latestKeys[k] = true
	}
	var older []string
	for k := range envMap {
		if !latestKeys[k] {
			older = append(older, k)
		}
	}
	sortStrings(older)
	for _, k := range older {
		if !seen[k] {
			out = append(out, k+"="+envMap[k])
			seen[k] = true
		}
	}
	for _, e := range latest {
		k, _, _ := strings.Cut(e, "=")
		if !seen[k] {
			out = append(out, k+"="+envMap[k])
			seen[k] = true
		}
	}
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		v := s[i]
		j := i - 1
		for j >= 0 && s[j] > v {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = v
	}
}

func applyLabel(s *libbuild.Stage, kvs []string) {
	if s.Config.Labels == nil {
		s.Config.Labels = map[string]string{}
	}
	for _, kv := range kvs {
		k, v, _ := strings.Cut(kv, "=")
		s.Config.Labels[k] = v
	}
}

func applyArg(scope map[string]string, in libbuild.Instruction) {
	for _, a := range in.Args {
		k, v, hasEq := strings.Cut(a, "=")
		if !hasEq {
			if _, exists := scope[k]; !exists {
				scope[k] = ""
			}
			continue
		}
		if _, exists := scope[k]; !exists {
			scope[k] = v
		}
	}
}

func applyExpose(s *libbuild.Stage, ports []string) {
	if s.Config.ExposedPorts == nil {
		s.Config.ExposedPorts = map[string]struct{}{}
	}
	for _, p := range ports {
		port := p
		if !strings.Contains(port, "/") {
			port += "/tcp"
		}
		s.Config.ExposedPorts[port] = struct{}{}
	}
}

func applyVolume(s *libbuild.Stage, vols []string) {
	if s.Config.Volumes == nil {
		s.Config.Volumes = map[string]struct{}{}
	}
	for _, v := range vols {
		s.Config.Volumes[v] = struct{}{}
	}
}

// applyCopy executes one COPY or ADD into the stage container's
// overlay upper. COPY --from=<stage> reads from a previous stage's
// live overlay upper; COPY --from=<image:tag> pulls and mounts a
// remote image transparently.
func applyCopy(in libbuild.Instruction, s *libbuild.Stage, sc *StageContainer, bc *libbuild.BuildContext, argScope map[string]string, req *BuildRequest) error {
	if len(in.Args) < 2 {
		return fmt.Errorf("%s requires at least one source and one destination", in.Kind)
	}
	args := expandAll(in.Args, stageVars(s, argScope))

	dst := args[len(args)-1]
	srcs := args[:len(args)-1]

	if !strings.HasPrefix(dst, "/") {
		hadSlash := strings.HasSuffix(dst, "/")
		dst = filepath.Join("/", s.Config.WorkingDir, dst)
		if hadSlash && !strings.HasSuffix(dst, "/") {
			dst += "/"
		}
	}

	srcRoot := bc.Root
	var excluded func(string) bool = bc.IsExcluded
	if from, ok := in.Flags["from"]; ok && from != "" {
		if other := libbuild.FindStage(req.Stages, from); other != nil {
			if other.Index >= s.Index {
				return fmt.Errorf("COPY --from=%q: stage %q is defined after the current stage", from, from)
			}
			prevSC := findStageContainer(req, other.Index)
			if prevSC == nil || prevSC.Cfg.RootfsDir == "" {
				return fmt.Errorf("COPY --from=%q: stage rootfs not available", from)
			}
			srcRoot = prevSC.Cfg.RootfsDir
		} else if containerimage.IsImageReference(from) {
			imgDir, err := resolveCopyFromImage(from)
			if err != nil {
				return fmt.Errorf("COPY --from=%q: %w", from, err)
			}
			srcRoot = imgDir
		} else {
			return fmt.Errorf("COPY --from=%q: no matching stage and not a valid image reference", from)
		}
		excluded = nil
	}

	return Apply(CopyParams{
		SrcRoot:  srcRoot,
		SrcPaths: srcs,
		Dst:      dst,
		DstRoot:  sc.WriteUpper(),
		Excluded: excluded,
	})
}

func stageVars(s *libbuild.Stage, argScope map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range argScope {
		out[k] = v
	}
	for _, e := range s.Config.Env {
		k, v, _ := strings.Cut(e, "=")
		out[k] = v
	}
	return out
}

func expandAll(args []string, vars map[string]string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = libbuild.Expand(a, vars)
	}
	return out
}

func writeConfigSidecar(sqfsPath string, cfg registry.RuntimeConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sqfsPath+".json", data, 0644)
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// resolveCopyFromImage pulls (or cache-hits) the given image reference
// and returns a directory on the host whose contents are the image's
// rootfs. Used for BuildKit-style `COPY --from=<image> src dst`.
func resolveCopyFromImage(ref string) (string, error) {
	progressCh := make(chan progress.Event, 16)
	renderDone := progress.StartRenderer(progressCh, os.Stderr)
	sqfsPath, err := containerimage.Pull(context.Background(), ref, env.BaseImageDir, progressCh)
	close(progressCh)
	<-renderDone
	if err != nil {
		return "", fmt.Errorf("pulling image: %w", err)
	}
	img, err := diskimage.Mount(sqfsPath)
	if err != nil {
		return "", fmt.Errorf("mounting image: %w", err)
	}
	return img.MountDir, nil
}
