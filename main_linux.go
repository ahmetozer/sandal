//go:build linux

package main

import (
	"fmt"
	"os"
	"time"

	"github.com/ahmetozer/sandal/pkg/cmd"
	containerguest "github.com/ahmetozer/sandal/pkg/container/guest"
	"github.com/ahmetozer/sandal/pkg/controller/embedded"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/vm/guest"
	"golang.org/x/sys/unix"
)

func platformMain() {
	// IsChild must be checked before IsVMInit: container child processes
	// in PID namespaces also see PID 1 and can read SANDAL_VM_ARGS from
	// /proc/cmdline, which would incorrectly trigger the VMInit path.
	if containerguest.IsChild() {
		containerguest.ContainerInitProc()
	} else if guest.IsVMInit() {
		if err := guest.VMInit(); err != nil {
			fmt.Fprintf(os.Stderr, "VMInit error: %v\n", err)
			unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)
			os.Exit(1)
		}
		// Redirect state directory to a local tmpfs path so the container
		// runtime can write config files for child processes, without
		// creating ghost entries on the host via VirtioFS.
		env.SetDefault("SANDAL_STATE_DIR", "/tmp/sandal-state")
		env.BaseStateDir = "/tmp/sandal-state"

		// Start the embedded controller API server and vsock listener so the
		// host can send management commands (exec, attach, snapshot, etc.).
		go embedded.StartEmbeddedController()
		go guest.StartControllerVsockListener()

		// Override ExitHandler so cmd.Main() triggers a VM power-off instead
		// of os.Exit(). The exit_linux.go init() may have missed SANDAL_VM_ARGS
		// because importKernelCmdlineEnv() hadn't run yet at package init time.
		cmd.ExitHandler = func(code int) {
			time.Sleep(50 * time.Millisecond)
			unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)
		}
		cmd.Main()
		// Let the PL011 tty drain buffered output before powering off.
		// The interrupt-driven TX needs CPU time to transmit remaining bytes.
		time.Sleep(50 * time.Millisecond)
		unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)
	} else {
		cmd.Main()
	}
}
