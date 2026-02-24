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
		}
		// PID 1 must not exit; power off the VM before os.Exit
		cmd.ExitHandler = func(code int) {
			unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)
		}
		cmd.Main()
	} else if cruntime.IsChild() {
		cruntime.ContainerInitProc()
	} else {
		cmd.Main()
	}
}
