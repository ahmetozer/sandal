//go:build linux

package run

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/vm/guest"
)

func Run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no command option provided")
	}
	if !guest.IsVMInit() && hasFlag(args, "vm") {
		return runInKVM(args)
	}
	return runContainer(args)
}
