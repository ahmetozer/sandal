//go:build linux

package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/inotify"
)

func (d DaemonConfig) loadByEvent() {
	watcher, err := inotify.New(env.BaseStateDir)
	if err != nil {
		slog.Error("loadByEvent", slog.String("action", "inotify init"), slog.Any("error", err))
		return
	}

	go func() {
		if err := watcher.Watch(); err != nil {
			slog.Error("loadByEvent", slog.Any("error", err))
		}
	}()

	// Handle events
	contName := ""
	for event := range watcher.Events {
		slog.Debug("loadByEvent", slog.Any("event", event.Event), slog.String("path", event.Path))
		// parse name from file if known event
		switch event.Event {
		case inotify.Modified, inotify.FileCreate, inotify.MovedFrom, inotify.Delete, inotify.MovedTo:
			// Strip the ".json" suffix instead of splitting on "." so names
			// that contain dots (allowed by ValidateName) are handled. The
			// old split-on-dot dropped every dotted-name state file, so the
			// daemon never processed their Delete and resurrected them (L6).
			name, ok := config.NameFromConfigFile(filepath.Base(event.Path))
			if !ok {
				slog.Warn("loadByEvent", slog.Any("error", "unknown name for state file"), slog.String("file", event.Path))
				continue
			}
			contName = name
		}

		switch event.Event {
		case inotify.FolderCreate:
			// Do nothing
		case inotify.Modified, inotify.FileCreate, inotify.MovedFrom:
			c, err := controller.LoadFile(event.Path)
			if err != nil {
				// Skip on load failure: a partial/concurrent write yields a nil
				// config, and SetContainer(nil) would panic the daemon.
				slog.Warn("loadByEvent", slog.Any("error", "load state file"), slog.String("file", event.Path), slog.Any("err", err))
				continue
			}
			controller.SetContainer(c)
		case inotify.Delete, inotify.MovedTo:
			controller.DeleteContainer(contName)
		case inotify.WatchStop:
			fmt.Println("Watch stopped")
			return
		default:
			slog.Info("loadByEvent", slog.Any("event", event.Event), slog.String("error", "unknown event"))
		}
	}
}

// superviseDiskEvents runs loadByEvent and restarts it if the inotify watcher
// dies (read/parse error, queue overflow that propagated, WatchStop). The
// watcher is the only event-driven path that syncs in-memory state with disk;
// if it died permanently, removals would be missed forever and startup
// containers resurrected (audit L8). On every restart it runs a full
// disk-authoritative reconcile to recover anything missed while it was down.
// It stops once the daemon is shutting down.
func (d DaemonConfig) superviseDiskEvents() {
	for !shuttingDown.Load() {
		d.loadByEvent()
		if shuttingDown.Load() {
			return
		}
		slog.Warn("loadByEvent", slog.String("msg", "watcher exited; reconciling and restarting"))
		reconcileState()
		time.Sleep(time.Second) // backoff to avoid a tight restart loop
	}
}

// reconcileState makes the daemon's in-memory container list converge to the
// authoritative on-disk state, independent of inotify health:
//   - DROP: an in-memory entry whose state file is gone was removed (the
//     watcher missed the Delete, or a dotted name slipped through an older
//     parser); dropping it stops the health-check from resurrecting it (L6/L8).
//   - ADD: a state file with no in-memory entry is a create the watcher missed;
//     load it so the container is tracked (and recoverable).
//
// SetContainer writes memory and disk together, so an entry can only be in
// memory if its file was written — therefore "in memory but no file" reliably
// means removed, with no lock check required.
func reconcileState() {
	mem, err := controller.Containers()
	if err != nil {
		slog.Warn("reconcile", slog.Any("error", err))
		return
	}

	entries, err := os.ReadDir(env.BaseStateDir)
	if err != nil {
		slog.Warn("reconcile", slog.String("action", "readdir"), slog.Any("error", err))
		return
	}
	onDisk := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if name, ok := config.NameFromConfigFile(e.Name()); ok {
			onDisk[name] = true
		}
	}

	inMem := make(map[string]bool, len(mem))
	for _, c := range mem {
		inMem[c.Name] = true
		if !onDisk[c.Name] {
			slog.Info("reconcile", slog.String("action", "drop"), slog.String("cont", c.Name), slog.String("reason", "state file removed"))
			controller.DeleteContainer(c.Name)
		}
	}

	for name := range onDisk {
		if inMem[name] {
			continue
		}
		c, lerr := controller.LoadFile(config.ConfigFileLoc(name))
		if lerr != nil {
			slog.Warn("reconcile", slog.String("action", "load"), slog.String("cont", name), slog.Any("error", lerr))
			continue
		}
		slog.Info("reconcile", slog.String("action", "add"), slog.String("cont", name))
		controller.SetContainer(c)
	}
}
