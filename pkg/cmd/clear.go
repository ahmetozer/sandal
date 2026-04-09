//go:build linux || darwin

package cmd

import (
	"flag"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/host"
	"github.com/ahmetozer/sandal/pkg/container/host/clean"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
	squash "github.com/ahmetozer/sandal/pkg/lib/container/image"
)

func Clear(args []string) error {

	f := flag.NewFlagSet("clear", flag.ExitOnError)

	var (
		help        bool
		deleteAll   bool
		dryRun      bool
		doImages    bool
		doSnapshots bool
		doOrphans   bool
		doKernel    bool
	)

	f.BoolVar(&help, "help", false, "show this help message")
	f.BoolVar(&deleteAll, "all", false, "reclaim everything: stopped containers, unused images and snapshots, orphan changedirs, stale kernel cache")
	f.BoolVar(&dryRun, "dry-run", false, "print what would be removed without deleting anything")
	f.BoolVar(&doImages, "images", false, "remove downloaded images under SANDAL_IMAGE_DIR that no container references")
	f.BoolVar(&doSnapshots, "snapshots", false, "remove snapshots under SANDAL_SNAPSHOT_DIR that no container references")
	f.BoolVar(&doOrphans, "orphans", false, "remove changedir files/dirs and ext4 .img files whose container state file is missing")
	f.BoolVar(&doKernel, "kernel-cache", false, "remove stale initramfs-sandal-*.img entries in SANDAL_KERNEL_DIR (keeps the most recent)")

	if err := f.Parse(args); err != nil {
		return fmt.Errorf("error parsing flags: %v", err)
	}

	if help {
		f.Usage()
		return nil
	}

	// -all is the catch-all: expand it to enable every scanner in
	// addition to stopped-container removal.
	if deleteAll {
		doImages = true
		doSnapshots = true
		doOrphans = true
		doKernel = true
	}

	conts, _ := controller.Containers()

	// Phase 1 — plan containers. Decide which containers will be
	// removed, honoring -all and c.Remove and skipping running
	// ones. We compute this *before* touching disk so that phase 2
	// (scanners) sees the post-removal usage set in both dry-run
	// and real modes — otherwise the preview disagrees with the
	// real run.
	var (
		containerActions []clean.Action
		toRemove         = map[string]bool{}
		toKeep           = make([]*config.Config, 0, len(conts))
	)
	for _, c := range conts {
		if !deleteAll {
			if !c.Remove {
				toKeep = append(toKeep, c)
				continue
			}
		}
		pid := c.ContPid
		if pid == 0 && c.VM != "" {
			pid = c.HostPid
		}
		isRunning, err := crt.IsPidRunning(pid)
		if err != nil {
			slog.Error("unable to get container status", "container", c.Name, "err", err)
		}
		if isRunning {
			slog.Warn("container is running", "container", c.Name, "rm", c.Remove)
			toKeep = append(toKeep, c)
			continue
		}
		if deleteAll {
			c.Remove = true
		}
		toRemove[c.Name] = true

		// Enumerate the same paths DeRunContainer would remove so
		// dry-run previews exactly match real behavior. Keep this
		// list in sync with pkg/container/host/derun*.go. Paths
		// that live outside the sandal-managed dirs are skipped
		// with a warning — we never touch user data.
		addPath := func(p, reason string) {
			if p == "" {
				return
			}
			if ok, _ := clean.IsInsideSandalArea(p); !ok {
				slog.Warn("clear: refusing to delete path outside sandal dirs", "container", c.Name, "path", p)
				return
			}
			containerActions = append(containerActions, clean.Action{
				Path:   p,
				Kind:   clean.KindContainer,
				Reason: reason,
			})
		}
		reason := "container " + c.Name
		addPath(c.RootfsDir, reason)
		addPath(c.ChangeDir, reason)
		if c.ChangeDir != "" {
			addPath(c.ChangeDir+".img", reason)
		}
		addPath(c.ConfigFileLoc(), reason)
	}

	// Build the usage set from the containers that will survive
	// phase 1. This enables chain-reclamation in a single pass:
	// when `sandal clear -all` removes mytest, an image referenced
	// only by mytest becomes eligible for removal in the same run.
	// The reason line on each action below is enriched with the
	// names of the containers being removed that referenced it, so
	// the report stays accurate ("only referenced by container(s)
	// being removed: mytest" rather than the misleading "not
	// referenced by any container").
	usage := clean.BuildUsageSet(toKeep)

	// Map image/snapshot path -> list of to-be-removed containers
	// that referenced it, so we can explain *why* it became
	// eligible after phase 1.
	refsByPath := map[string][]string{}
	for _, c := range conts {
		if !toRemove[c.Name] {
			continue
		}
		for _, im := range c.ImmutableImages {
			if im.File == "" {
				continue
			}
			refsByPath[im.File] = append(refsByPath[im.File], c.Name)
		}
		for _, ref := range c.Lower {
			p := filepath.Join(env.BaseImageDir, squash.SanitizeRef(ref)+".sqfs")
			refsByPath[p] = append(refsByPath[p], c.Name)
		}
		if c.Snapshot != "" {
			refsByPath[c.Snapshot] = append(refsByPath[c.Snapshot], c.Name)
		}
		refsByPath[filepath.Join(env.BaseSnapshotDir, c.Name+".sqfs")] = append(
			refsByPath[filepath.Join(env.BaseSnapshotDir, c.Name+".sqfs")], c.Name)
	}
	// enrichReason mutates Action.Reason in place so the report
	// explains which removed container(s) used to reference it.
	enrichReason := func(as []clean.Action) []clean.Action {
		for i := range as {
			names := refsByPath[as[i].Path]
			if len(names) == 0 {
				continue
			}
			seen := map[string]bool{}
			uniq := names[:0]
			for _, n := range names {
				if seen[n] {
					continue
				}
				seen[n] = true
				uniq = append(uniq, n)
			}
			as[i].Reason = "only referenced by: " + strings.Join(uniq, ", ")
		}
		return as
	}

	// For the real run, perform phase-1 removals now via
	// DeRunContainer so it can also tear down mounts, cgroups, and
	// network interfaces. The action list above is used only for
	// the printed report.
	if !dryRun {
		for _, c := range conts {
			if !toRemove[c.Name] {
				continue
			}
			host.DeRunContainer(c)
		}
	}

	// Track paths already accounted for in containerActions so the
	// scanners don't re-enumerate them as orphans (which they
	// correctly *are*, post-removal — but they'd show up twice in
	// the dry-run report).
	seen := map[string]bool{}
	for _, a := range containerActions {
		seen[a.Path] = true
	}
	dedup := func(in []clean.Action) []clean.Action {
		out := in[:0]
		for _, a := range in {
			if seen[a.Path] {
				continue
			}
			seen[a.Path] = true
			out = append(out, a)
		}
		return out
	}

	var actions []clean.Action
	// In dry-run, container paths need to appear in Apply's output
	// as "would remove" lines. In real-run, DeRunContainer has
	// already done the work, so we only need to report it once
	// below — skip feeding containerActions into Apply.
	if dryRun {
		actions = append(actions, containerActions...)
	}
	if doOrphans {
		actions = append(actions, dedup(clean.PlanOrphans(usage))...)
	}
	if doImages {
		actions = append(actions, dedup(enrichReason(clean.PlanImages(usage)))...)
	}
	if doSnapshots {
		actions = append(actions, dedup(enrichReason(clean.PlanSnapshots(usage)))...)
	}
	if doKernel {
		actions = append(actions, dedup(clean.PlanKernelCache())...)
	}

	// Report container removals that already happened in the real
	// run so the user gets a single consistent summary.
	if !dryRun {
		for _, a := range containerActions {
			fmt.Printf("removed [%s] %s  (%s)\n", a.Kind, a.Path, a.Reason)
		}
	}

	if len(actions) == 0 && len(containerActions) == 0 {
		fmt.Println("clear: nothing to remove")
		return nil
	}

	count, bytes := clean.Apply(actions, dryRun)
	if !dryRun {
		// DeRunContainer already processed these; still account
		// for them in the summary count.
		count += len(containerActions)
	}
	verb := "removed"
	if dryRun {
		verb = "would remove"
	}
	fmt.Printf("clear: %s %d item(s), %s reclaimed\n", verb, count, humanBytesCmd(bytes))
	return nil
}

// humanBytesCmd mirrors clean.humanBytes for report formatting at the
// command layer.
func humanBytesCmd(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}
