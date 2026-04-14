//go:build darwin

package sandal

import (
	"github.com/ahmetozer/sandal/pkg/lib/container/build"
)

// runBuild on darwin always dispatches to the VZ VM. Native container
// builds require Linux namespaces + overlayfs, so macOS must always
// go through a VZ VM. The `-vm` flag is implicit here.
func runBuild(opts BuildOpts, dfPath string, globalArgs []build.Instruction, stages []*build.Stage) (string, error) {
	_ = dfPath
	_ = globalArgs
	_ = stages
	return buildInVZ(opts)
}
