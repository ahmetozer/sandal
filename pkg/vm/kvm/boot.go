//go:build linux && (amd64 || arm64)

package kvm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/container/forward"
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/mgmt"
	"github.com/ahmetozer/sandal/pkg/vm/terminal"
	"golang.org/x/sys/unix"
)

// Boot boots a VM using KVM with the given name and configuration.
// It sets up the terminal, creates the VM, relays I/O, and blocks until
// the VM stops or a signal is received.
// If stdin/stdout are nil, os.Stdin/os.Stdout are used with raw terminal mode.
func Boot(name string, cfg vmconfig.VMConfig, stdin io.Reader, stdout io.Writer) error {
	return BootWithForwards(name, cfg, stdin, stdout, nil)
}

// BootWithForwards is Boot plus a port-forwarding mapping list. When forwards
// is non-empty, the host starts per-mapping listeners that tunnel to the guest
// via AF_VSOCK. Cleanup is deferred to VM stop.
func BootWithForwards(name string, cfg vmconfig.VMConfig, stdin io.Reader, stdout io.Writer, forwards []forward.PortMapping) error {
	// Write PID file
	if err := vmconfig.WritePidFile(name); err != nil {
		slog.Warn("Boot", slog.String("action", "write pid file"), slog.Any("error", err))
	}
	defer vmconfig.RemovePidFile(name)

	customIO := stdin != nil && stdout != nil
	if stdin == nil {
		stdin = os.Stdin
	}
	if stdout == nil {
		stdout = os.Stdout
	}

	// Set terminal to raw mode for serial console (skip if custom I/O provided)
	restore := func() {}
	if !customIO {
		var err error
		restore, err = terminal.SetRaw()
		if err != nil {
			slog.Warn("Boot", slog.String("action", "set raw terminal"), slog.Any("error", err))
			restore = func() {}
		}
	}
	defer func() {
		restore()
		if !customIO {
			exec.Command("stty", "sane").Run()
		}
	}()

	vm, err := NewVM(name, cfg)
	if err != nil {
		restore()
		return fmt.Errorf("creating VM: %w", err)
	}
	defer vm.Close()

	// Relay serial console I/O
	vm.StartIORelay(stdin, stdout)

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		vm.RequestStop()
		<-sigCh
		vm.Stop()
	}()

	// Start VM
	if err := vm.Start(); err != nil {
		restore()
		return fmt.Errorf("starting VM: %w", err)
	}
	slog.Debug("Boot", slog.String("action", "started"), slog.String("state", vm.State().String()))

	// Start the management socket relay so host commands (e.g. sandal exec)
	// can reach the embedded controller inside the VM via AF_VSOCK.
	// Use this VM's actual vsock CID (allocated dynamically in NewVM) so
	// that with multiple concurrent VMs the per-container Unix socket relays
	// to the correct guest instead of always racing for CID 3.
	mgmtCleanup := mgmt.StartManagementSocket(name, mgmt.VsockConnector{GuestCID: uint32(vm.VsockGuestCID()), Port: 4000})
	defer mgmtCleanup()

	// Port-forwarding listeners — host side only. The guest relay is started
	// from SANDAL_VM_FORWARDS during guest init. Matching vsock ports are
	// derived from each mapping's id via VsockBasePort.
	//
	// If the host bind fails (EADDRINUSE, permission, etc.) we return an
	// error instead of continuing — a VM whose forwards don't work is
	// usually worse than a fast failure: the user sees the container
	// "running" but can't reach it, and the cause (a stale sandal still
	// holding the port, a foreground run in another terminal, etc.) is
	// buried in VM boot output.
	if len(forwards) > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		stop, err := forward.Start(ctx, name, forwards,
			forward.VsockTransport{GuestCID: uint32(vm.VsockGuestCID())})
		if err != nil {
			cancel()
			return portForwardError(forwards, err)
		}
		defer cancel()
		defer stop()
	}

	// Wait for VM to stop
	if err := vm.WaitUntilStopped(); err != nil {
		slog.Error("Boot", slog.String("action", "stopped"), slog.Any("error", err))
	} else {
		slog.Debug("Boot", slog.String("action", "stopped"))
	}

	return nil
}

// portForwardError wraps a forward.Start failure with context the user
// actually needs: which mapping, likely cause, and how to investigate.
// The most common failure is EADDRINUSE when another sandal run (or any
// other process) is still listening on the same host port.
func portForwardError(forwards []forward.PortMapping, err error) error {
	var specs []string
	for _, f := range forwards {
		specs = append(specs, f.Raw)
	}
	msg := err.Error()
	hint := ""
	if strings.Contains(msg, "address already in use") || errors.Is(err, syscall.EADDRINUSE) {
		hint = "\n  hint: another process is listening on the host port. Common causes:" +
			"\n    • a previous `sandal run` with the same -p is still running (check `sandal ps`)" +
			"\n    • a foreground run in another terminal that was Ctrl-C'd but left the listener" +
			"\n    • another service on the host using the same port (check `ss -tlnp | grep <port>`)"
	}
	return fmt.Errorf("port-forward setup failed for %v: %w%s", specs, err, hint)
}

// vsockAvailable returns true if the host can communicate with KVM guests
// via AF_VSOCK. This requires /dev/vhost-vsock which provides the vhost
// backend for virtio-vsock devices in KVM VMs.
func vsockAvailable() bool {
	fd, err := unix.Open("/dev/vhost-vsock", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	unix.Close(fd)
	return true
}
