//go:build !linux && !darwin

package sandal

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/lib/container/build"
)

// runBuild is a stub for platforms without a build backend.
func runBuild(opts BuildOpts, dfPath string, globalArgs []build.Instruction, stages []*build.Stage) (string, error) {
	_ = opts
	_ = dfPath
	_ = globalArgs
	_ = stages
	return "", fmt.Errorf("sandal build is not supported on this platform")
}
