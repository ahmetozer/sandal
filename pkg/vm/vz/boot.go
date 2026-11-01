//go:build darwin

package vz

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/container/forward"
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/mgmt"
	"github.com/ahmetozer/sandal/pkg/vm/terminal"
)

// Boot boots the VM without applying any cloud-init or initrd overlays.
// If relays is non-empty, vsock listeners are set up to relay guest connections
// to host Unix sockets (used for -v socket sharing).
func Boot(name string, cfg vmconfig.VMConfig, relays ...SocketRelay) error {
	return BootWithForwards(name, cfg, nil, nil, nil, relays...)
}

// BootWithForwards is Boot plus port-forwarding support. When forwards is
// non-empty, the host starts per-mapping listeners that tunnel to the guest
// via VZ's vsock API after the VM starts.
// The serial console is relayed to stdin/stdout. If stdin/stdout are nil,
// os.Stdin/os.Stdout are used with raw terminal mode, as in kvm.Boot.
func BootWithForwards(name string, cfg vmconfig.VMConfig, stdin io.Reader, stdout io.Writer, forwards []forward.PortMapping, relays ...SocketRelay) error {
	// Kill stale VM processes holding the disk file
	if cfg.DiskPath != "" {
		if killed, err := killStaleDiskHolders(cfg.DiskPath); err != nil {
			slog.Warn("Boot", slog.String("action", "check stale processes"), slog.Any("error", err))
		} else if killed > 0 {
			slog.Info("Boot", slog.String("action", "killed stale processes"), slog.Int("count", killed), slog.String("disk", cfg.DiskPath))
		}
	}

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

	// Set terminal to raw mode for serial console (skip if not a TTY, or
	// if custom I/O provided)
	restore := func() {}
	if !customIO {
		var err error
		restore, err = terminal.SetRaw()
		if err != nil {
			slog.Warn("Boot", slog.String("action", "set raw terminal"), slog.Any("error", err))
			restore = func() {} // no-op
		}
	}
	// Ensure terminal is always restored, even on signals
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

	// Port-forwarding listeners — host side. Bind them before the VM
	// starts so a host port that is already in use fails the run with an
	// error, as on KVM, instead of a warning next to a running VM that
	// cannot be reached. Connections are only dialled into the guest
	// (vsock) after they are accepted; the guest relay is started from
	// crun inside the VM via forward.StartVsock (same as KVM).
	stopForwardFunc := func() {}
	if len(forwards) > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		stop, err := forward.Start(ctx, name, forwards, forward.VZTransport{VM: vm})
		if err != nil {
			cancel()
			restore()
			return fmt.Errorf("port-forward setup failed: %w", err)
		}
		var once sync.Once
		stopForwardFunc = func() {
			once.Do(func() {
				cancel()
				stop()
			})
		}
	}
	// The listeners are bound already: close them on every return,
	// including a vm.Start that fails before the stop watcher runs.
	defer stopForwardFunc()

	// Set up vsock relays for socket sharing (before start)
	if len(relays) > 0 {
		vm.SetupVsockRelays(relays)
	}

	// Relay serial console I/O between VM and host stdin/stdout
	vm.StartIORelay(stdin, stdout)

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		vm.RequestStop()
		<-sigCh
		vm.Stop()
		StopMainRunLoop()
	}()

	// Start VM asynchronously. A start error is returned once the run
	// loop ends, so the run exits non-zero.
	startErr := make(chan error, 1)
	go func() {
		if err := vm.Start(); err != nil {
			restore()
			startErr <- err
			StopMainRunLoop()
			return
		}
		slog.Debug("Boot", slog.String("action", "started"), slog.String("state", vm.State().String()))

		// Start the management socket relay so macOS commands can reach the
		// embedded controller inside the VM via vsock port 4000.
		mgmt.StartManagementSocket(name, mgmt.VZConnector{VM: vm, Port: 4000})
	}()

	// Wait for VM to stop
	go func() {
		err := vm.WaitUntilStopped()
		if err != nil {
			slog.Error("Boot", slog.String("action", "stopped"), slog.Any("error", err))
		} else {
			slog.Debug("Boot", slog.String("action", "stopped"))
		}
		// Clean up port-forwarding listeners before stopping the run loop.
		stopForwardFunc()
		StopMainRunLoop()
	}()

	// Run the main run loop (blocks until StopMainRunLoop)
	RunMainRunLoop()
	select {
	case err := <-startErr:
		return fmt.Errorf("starting VM: %w", err)
	default:
		return nil
	}
}

func killStaleDiskHolders(diskPath string) (int, error) {
	absPath, err := filepath.Abs(diskPath)
	if err != nil {
		absPath = diskPath
	}

	out, err := exec.Command("lsof", "-t", absPath).Output()
	if err != nil {
		return 0, nil
	}

	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return 0, nil
	}

	myPID := os.Getpid()
	killed := 0
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil {
			continue
		}
		if pid == myPID {
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		if err := proc.Kill(); err == nil {
			proc.Wait()
			killed++
		}
	}
	return killed, nil
}
