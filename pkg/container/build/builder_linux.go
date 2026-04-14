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
	"github.com/ahmetozer/sandal/pkg/lib/squashfs"
)

// BuildRequest is the runtime input for a build.
type BuildRequest struct {
	GlobalArgs []libbuild.Instruction // pre-FROM ARGs
	Stages     []*libbuild.Stage
	BuildArgs  map[string]string  // --build-arg
	ContextDir string
	Tag        string
	Target     string             // --target stage name; "" means last
	Push       bool
	Platform   registry.Platform

	buildID string // generated; used for unique container names
	stepIdx int    // monotonic counter for naming RUN containers
}

// Run executes the build and returns the local sqfs cache path of the
// final image. If req.Push is true, the image is also pushed to the
// registry implied by req.Tag.
func Run(ctx context.Context, req BuildRequest) (string, error) {
	if len(req.Stages) == 0 {
		return "", fmt.Errorf("no stages")
	}
	if req.buildID == "" {
		req.buildID = genBuildID()
	}

	// Open build context (loads .dockerignore).
	bc, err := libbuild.NewBuildContext(req.ContextDir)
	if err != nil {
		return "", err
	}

	// Build-arg scope: --build-arg overrides ARG defaults declared in the
	// Dockerfile (we let Dockerfile ARGs be the keys; --build-arg supplies
	// the value).
	argScope := map[string]string{}
	// Apply pre-FROM ARGs (their declarations) so FROM expansion sees defaults.
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
			return "", fmt.Errorf("stage %d: %w", i, err)
		}
	}

	target := req.Stages[targetIdx]

	// Final output: write merged rootfs as squashfs in the image cache.
	outPath := filepath.Join(env.BaseImageDir, containerimage.SanitizeRef(req.Tag)+".sqfs")
	if err := os.MkdirAll(env.BaseImageDir, 0755); err != nil {
		return "", err
	}
	if err := writeSquashfs(target.RootfsDir, outPath); err != nil {
		return "", fmt.Errorf("writing %s: %w", outPath, err)
	}

	// Sidecar JSON for runtime config.
	if err := writeConfigSidecar(outPath, target.Config); err != nil {
		return "", fmt.Errorf("writing config sidecar: %w", err)
	}

	// Push FIRST (while rootfs dirs still exist), then cleanup.
	if req.Push {
		if err := libbuild.Push(ctx, libbuild.PushOpts{
			RootfsDir: target.RootfsDir,
			Tag:       req.Tag,
			Config:    target.Config,
			History:   target.History,
			Platform:  req.Platform,
		}); err != nil {
			cleanupStageRoots(req.Stages)
			return "", fmt.Errorf("push %s: %w", req.Tag, err)
		}
	}

	cleanupStageRoots(req.Stages)
	return outPath, nil
}

// cleanupStageRoots unmounts and removes each stage's working rootfs.
func cleanupStageRoots(stages []*libbuild.Stage) {
	for _, s := range stages {
		if s.RootfsDir == "" {
			continue
		}
		_ = UnmountStageRoot(s.RootfsDir)
		os.RemoveAll(s.RootfsDir)
	}
}

// buildStage materialises one stage: pulls its base, applies all
// instructions, and leaves the merged rootfs at s.RootfsDir.
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

	// Resolve base: either a previous stage or an OCI registry image.
	baseDir, baseConfig, err := resolveBase(ctx, baseRef, req.Stages, s.Index, req.Platform)
	if err != nil {
		return fmt.Errorf("resolving base %s: %w", baseRef, err)
	}
	s.Config = baseConfig
	s.History = append(s.History, registry.History{
		Created:   nowRFC3339(),
		CreatedBy: "FROM " + baseRef,
		EmptyLayer: true,
	})

	// Stage rootfs: a directory we own that will accumulate all
	// instruction results. It MUST NOT be on overlayfs — the kernel
	// rejects nested overlay stacks, and each RUN step layers another
	// overlay on top. Use a freshly-mounted tmpfs to guarantee a real
	// filesystem regardless of the host's choice for /var/lib/sandal.
	stageRoot, err := os.MkdirTemp(env.BaseTempDir, fmt.Sprintf("sandal-build-stage-%d-*", s.Index))
	if err != nil {
		return err
	}
	if err := mountStageRootTmpfs(stageRoot); err != nil {
		os.Remove(stageRoot)
		return fmt.Errorf("mounting stage tmpfs: %w", err)
	}
	s.RootfsDir = stageRoot

	// Snapshot base into the stage rootfs.
	if err := snapshotBase(baseDir, stageRoot); err != nil {
		return fmt.Errorf("snapshot base: %w", err)
	}

	// Apply instructions in order.
	for _, in := range s.Instrs {
		if err := applyInstruction(ctx, in, s, bc, argScope, req); err != nil {
			return fmt.Errorf("line %d (%s): %w", in.Line, in.Kind, err)
		}
	}
	return nil
}

// resolveBase returns the on-disk rootfs directory and base config for
// either "scratch", a previous stage, or a registry image.
func resolveBase(ctx context.Context, ref string, stages []*libbuild.Stage, currentIdx int, platform registry.Platform) (string, registry.RuntimeConfig, error) {
	// FROM scratch — empty rootfs.
	if strings.EqualFold(ref, "scratch") {
		dir, err := os.MkdirTemp(env.BaseTempDir, "sandal-build-scratch-*")
		return dir, registry.RuntimeConfig{}, err
	}
	// FROM <previous-stage-name-or-index>
	for _, prev := range stages[:currentIdx] {
		if prev.Name == ref || fmt.Sprintf("%d", prev.Index) == ref {
			if prev.RootfsDir == "" {
				return "", registry.RuntimeConfig{}, fmt.Errorf("FROM %s: stage has no rootfs (not yet built)", ref)
			}
			return prev.RootfsDir, prev.Config, nil
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
	img, err := diskimage.Mount(sqfsPath)
	if err != nil {
		return "", registry.RuntimeConfig{}, fmt.Errorf("mounting %s: %w", sqfsPath, err)
	}
	cfg, _ := containerimage.LoadImageConfig(sqfsPath)
	var rt registry.RuntimeConfig
	if cfg != nil {
		rt = *cfg
	}
	// Note: img.MountDir is leaked until process exit — sandal already
	// does this for pull cache mounts; the immutable image dir is GC'd
	// on next sandal startup.
	return img.MountDir, rt, nil
}

// snapshotBase copies the CONTENTS of baseDir into stageRoot (not baseDir
// itself as a subdir). Symlinks are preserved. We use a regular file copy
// (not overlayfs) to keep stageRoot fully owned by the build — RUN/COPY
// can mutate it freely.
func snapshotBase(baseDir, stageRoot string) error {
	return copyTree(baseDir, stageRoot, "", nil)
}

// applyInstruction dispatches one parsed instruction to the appropriate handler.
func applyInstruction(ctx context.Context, in libbuild.Instruction, s *libbuild.Stage, bc *libbuild.BuildContext, argScope map[string]string, req *BuildRequest) error {
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
			// Resolve against existing WorkingDir if relative.
			if !strings.HasPrefix(wd, "/") {
				wd = filepath.Join("/", s.Config.WorkingDir, wd)
			}
			s.Config.WorkingDir = wd
			// Materialise the directory so subsequent RUN/COPY can use it.
			abs := filepath.Join(s.RootfsDir, wd)
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
		// Shell-form RUN/CMD/ENTRYPOINT shell — recorded but not yet used.
		// Phase 3 will honour this when wrapping shell-form RUN.
	case libbuild.InstrCopy, libbuild.InstrAdd:
		if err := applyCopy(in, s, bc, argScope, req); err != nil {
			return err
		}
	case libbuild.InstrRun:
		req.stepIdx++
		args := instructionArgs(in, nil) // SHELL override deferred
		if err := ExecRun(RunOpts{
			StageRoot: s.RootfsDir,
			BuildID:   req.buildID,
			StepIdx:   req.stepIdx,
			Args:      args,
			WorkDir:   s.Config.WorkingDir,
			User:      s.Config.User,
			Env:       s.Config.Env,
		}); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported instruction %s", in.Kind)
	}

	// Record History entry for non-empty instructions.
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
	// Merge starting scope for expansion: current env + ARG scope.
	// Values set earlier in THIS ENV instruction win when referenced later.
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

// mapToEnvList rebuilds the env slice preserving original order with
// duplicates collapsed. New keys are appended in declaration order from
// the most recent ENV.
func mapToEnvList(envMap map[string]string, latest []string) []string {
	seen := map[string]bool{}
	var out []string
	// Preserve previously-seen order by reading old slice.
	// Caller doesn't pass it in here; we approximate by emitting latest-first
	// then any leftovers. For now: emit in deterministic alphabetical order
	// for the keys NOT touched by latest, then the latest (in its order).
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
	// Stable order: simple sort to keep tests deterministic.
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
	// Insertion sort — env lists are tiny.
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
			// Declare without default — only valid if --build-arg or env supplies it.
			if _, exists := scope[k]; !exists {
				scope[k] = ""
			}
			continue
		}
		// Don't override if user supplied a --build-arg (those win).
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

func applyCopy(in libbuild.Instruction, s *libbuild.Stage, bc *libbuild.BuildContext, argScope map[string]string, req *BuildRequest) error {
	if len(in.Args) < 2 {
		return fmt.Errorf("%s requires at least one source and one destination", in.Kind)
	}
	args := expandAll(in.Args, stageVars(s, argScope))

	// Last arg is destination, the rest are sources.
	dst := args[len(args)-1]
	srcs := args[:len(args)-1]

	// Per Docker semantics: a relative destination is resolved against
	// the current WorkingDir. Preserve the trailing "/" if present, since
	// it controls dir-vs-file semantics in Apply().
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
		// Multi-stage source: resolve to that stage's rootfs.
		other := libbuild.FindStage(req.Stages, from)
		if other == nil {
			return fmt.Errorf("COPY --from=%q: stage not found", from)
		}
		if other.Index >= s.Index {
			return fmt.Errorf("COPY --from=%q: stage %q is defined after the current stage", from, from)
		}
		if other.RootfsDir == "" {
			return fmt.Errorf("COPY --from=%q: stage rootfs not available (not built?)", from)
		}
		srcRoot = other.RootfsDir
		excluded = nil // .dockerignore only applies to the build context
	}

	return Apply(CopyParams{
		SrcRoot:  srcRoot,
		SrcPaths: srcs,
		Dst:      dst,
		DstRoot:  s.RootfsDir,
		Excluded: excluded,
	})
}

// stageVars returns the variable map for ${VAR} expansion: ENV merged
// with build-time ARG scope. ENV wins on duplicate keys.
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

func writeSquashfs(srcDir, outPath string) error {
	tmp, err := os.CreateTemp(filepath.Dir(outPath), ".build-*.sqfs.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	w, err := squashfs.NewWriter(tmp, squashfs.WithCompression(squashfs.CompGzip))
	if err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := w.CreateFromDir(srcDir); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	tmp.Close()
	return os.Rename(tmpPath, outPath)
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
