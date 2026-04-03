//go:build darwin

package sandal

import (
	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/env"
)

// platformRun on macOS always boots a VM (no native container support).
func platformRun(args []string) error {
	c := config.NewContainer()
	c.HostArgs = append([]string{env.BinLoc, "run"}, args...)
	name, _ := ExtractFlag(args, "name", "")
	if name != "" {
		c.Name = name
	}
	return RunInVM(&c)
}
