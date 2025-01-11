package daemon

import (
	"fmt"
	"log"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/tools/inotify"
)

func (d DaemonConfig) loadByEvent() {
	watcher, err := inotify.New(env.BaseStateDir)
	if err != nil {
		log.Fatal(err)
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
			fileName := strings.Split(filepath.Base(event.Path), ".")
			if len(fileName) != 2 && event.Event != inotify.WatchStop {
				slog.Warn("loadByEvent", slog.Any("error", "unknown name for state file"), slog.String("file", event.Path))
				continue
			}
			contName = fileName[0]
		}

		switch event.Event {
		case inotify.FolderCreate:
			// Do nothing
		case inotify.Modified, inotify.FileCreate, inotify.MovedFrom:
			c, err := controller.LoadFile(event.Path)
			if err != nil {
				slog.Warn("loadByEvent", slog.Any("error", "unknown name for state file"), slog.String("file", event.Path))
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
