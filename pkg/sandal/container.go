//go:build linux

package sandal

import (
	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/host"
)

// RunContainer is kept here for backward compatibility with any
// external callers that import pkg/sandal. The canonical
// implementation lives in pkg/container/host.RunContainer — this is
// a thin forwarder.
//
// It used to live here, but pkg/container/build needs to call it and
// pkg/sandal imports pkg/container/build (for the builder dispatch),
// which would create an import cycle. The body was relocated to
// pkg/container/host (already imported by both) to break the cycle.
func RunContainer(c *config.Config, networkFlags []string) error {
	return host.RunContainer(c, networkFlags)
}
