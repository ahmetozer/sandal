//go:build linux

package kvm

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/mgmt"
	"github.com/ahmetozer/sandal/pkg/vm/terminal"
)

// Boot boots a VM using KVM with the given name and configuration.
// It sets up the terminal, creates the VM, relays I/O, and blocks until
// the VM stops or a signal is received.
// If stdin/stdout are nil, os.Stdin/os.Stdout are used with raw terminal mode.
func Boot(name string, cfg vmconfig.VMConfig, stdin io.Reader, stdout io.Writer) error {
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

	// Start the management socket relay so host commands can reach the
	// embedded controller inside the VM via AF_VSOCK (CID=3, port=4000).
	mgmtCleanup := mgmt.StartManagementSocket(name, mgmt.VsockConnector{GuestCID: 3, Port: 4000})
	defer mgmtCleanup()

	// Wait for VM to stop
	if err := vm.WaitUntilStopped(); err != nil {
		slog.Error("Boot", slog.String("action", "stopped"), slog.Any("error", err))
	} else {
		slog.Debug("Boot", slog.String("action", "stopped"))
	}

	return nil
}
