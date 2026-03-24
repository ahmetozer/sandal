//go:build linux

package main

import (
	"fmt"
	"os"

	"github.com/ahmetozer/sandal/pkg/cmd"
	"github.com/ahmetozer/sandal/pkg/container/cruntime"
	"github.com/ahmetozer/sandal/pkg/vm/guest"
	"golang.org/x/sys/unix"
)

func platformMain() {
	if guest.IsVMInit() {
		if err := guest.VMInit(); err != nil {
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
