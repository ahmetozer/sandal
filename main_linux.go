//go:build linux

package main

import (
	"fmt"
	"os"

	"github.com/ahmetozer/sandal/pkg/cmd"
	"github.com/ahmetozer/sandal/pkg/container/cruntime"
	"golang.org/x/sys/unix"
)

func platformMain() {
	if cruntime.IsVMInit() {
		if err := cruntime.VMInit(); err != nil {
			fmt.Fprintf(os.Stderr, "VMInit error: %v\n", err)
			unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)
			os.Exit(1)
		}
		cmd.Main()
		// PID 1 must never return — power off the VM
		unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)
	} else if cruntime.IsChild() {
		cruntime.ContainerInitProc()
	} else {
		cmd.Main()
	}
}
