//go:build !linux

package sandal

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/lib/container/build"
)

// runBuild on non-linux platforms returns an error. macOS support will be
// added later by dispatching to the VM build path.
func runBuild(opts BuildOpts, dfPath string, globalArgs []build.Instruction, stages []*build.Stage) (string, error) {
	return "", fmt.Errorf("sandal build is not yet supported on this platform (only linux); use VM mode when implemented")
}
