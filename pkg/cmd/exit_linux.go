//go:build linux

package cmd

import (
	"os"

	cnet "github.com/ahmetozer/sandal/pkg/container/net"
	"golang.org/x/sys/unix"
)

func init() {
	ExitHandler = func(code int) {
		cnet.CancelAllDHCPv6Loops()
		os.Exit(code)
	}
	if vmArgs := os.Getenv("SANDAL_VM_ARGS"); vmArgs != "" {
		ExitHandler = func(code int) {
			cnet.CancelAllDHCPv6Loops()
			unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)
		}
	}
}
