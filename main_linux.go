//go:build linux

package main

import (
	"fmt"
	"os"

	"github.com/ahmetozer/sandal/pkg/cmd"
	"github.com/ahmetozer/sandal/pkg/container/cruntime"
)

func platformMain() {
	if cruntime.IsVMInit() {
		if err := cruntime.VMInit(); err != nil {
			fmt.Fprintf(os.Stderr, "VMInit error: %v\n", err)
		}
		// PID 1 must not exit; power off the VM before os.Exit
		cmd.Main()
	} else if cruntime.IsChild() {
		cruntime.ContainerInitProc()
	} else {
		cmd.Main()
	}
}
