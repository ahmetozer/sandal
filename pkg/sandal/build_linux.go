//go:build linux

package sandal

import (
	"context"
	"runtime"

	containerbuild "github.com/ahmetozer/sandal/pkg/container/build"
	libbuild "github.com/ahmetozer/sandal/pkg/lib/container/build"
	"github.com/ahmetozer/sandal/pkg/lib/container/registry"
)

// runBuild dispatches to the linux builder.
func runBuild(opts BuildOpts, dfPath string, globalArgs []libbuild.Instruction, stages []*libbuild.Stage) (string, error) {
	_ = dfPath // path already opened by caller; kept for future caching
	req := containerbuild.BuildRequest{
		GlobalArgs: globalArgs,
		Stages:     stages,
		BuildArgs:  opts.BuildArgs,
		ContextDir: opts.ContextDir,
		Tag:        opts.Tag,
		Target:     opts.Target,
		Push:       opts.Push,
		Platform: registry.Platform{
			OS:           "linux",
			Architecture: runtime.GOARCH,
		},
	}
	return containerbuild.Run(context.Background(), req)
}
