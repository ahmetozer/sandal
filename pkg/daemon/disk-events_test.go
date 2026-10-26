//go:build linux

package daemon

import (
	"testing"

	"github.com/ahmetozer/sandal/pkg/lib/inotify"
)

func TestDiskEventAction(t *testing.T) {
	cases := []struct {
		ev   inotify.EventType
		base string
		want diskAction
	}{
		{inotify.MovedTo, "nginx.json", actionLoad},
		{inotify.MovedFrom, "nginx.json", actionDelete},
		{inotify.Modified, "nginx.json", actionLoad},
		{inotify.FileCreate, "nginx.json", actionLoad},
		{inotify.Delete, "nginx.json", actionDelete},
		{inotify.MovedTo, ".nginx.json.tmp-1234", actionIgnore},
		{inotify.FileCreate, ".nginx.json.tmp-1234", actionIgnore},
		{inotify.Modified, "notes.txt", actionUnknown},
	}
	for _, c := range cases {
		if got := diskEventAction(c.ev, c.base); got != c.want {
			t.Errorf("diskEventAction(%v, %q) = %v, want %v", c.ev, c.base, got, c.want)
		}
	}
}
