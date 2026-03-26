//go:build linux

package cmd

import (
	"os"

	"golang.org/x/sys/unix"
)

func init() {
	ExitHandler = func(code int) {
		os.Exit(code)
	}
	if vmArgs := os.Getenv("SANDAL_VM_ARGS"); vmArgs != "" {
		ExitHandler = func(code int) {
			unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)
		}
	}
}
