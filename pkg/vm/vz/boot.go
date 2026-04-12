//go:build darwin

package vz

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
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
	return BootWithForwards(name, cfg, nil, relays...)
}

// BootWithForwards is Boot plus port-forwarding support. When forwards is
// non-empty, the host starts per-mapping listeners that tunnel to the guest
// via VZ's vsock API after the VM starts.
func BootWithForwards(name string, cfg vmconfig.VMConfig, forwards []forward.PortMapping, relays ...SocketRelay) error {
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

	// Set terminal to raw mode for serial console (skip if not a TTY)
	restore, err := terminal.SetRaw()
	if err != nil {
		slog.Warn("Boot", slog.String("action", "set raw terminal"), slog.Any("error", err))
		restore = func() {} // no-op
	}
	// Ensure terminal is always restored, even on signals
	defer func() {
		restore()
		exec.Command("stty", "sane").Run()
	}()

	vm, err := NewVM(name, cfg)
	if err != nil {
		restore()
		return fmt.Errorf("creating VM: %w", err)
	}

	// Set up vsock relays for socket sharing (before start)
	if len(relays) > 0 {
		vm.SetupVsockRelays(relays)
	}

	// Relay serial console I/O between VM and host stdin/stdout
	vm.StartIORelay(os.Stdin, os.Stdout)

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

	// Start VM asynchronously
	go func() {
		if err := vm.Start(); err != nil {
			restore()
			slog.Error("Boot", slog.String("action", "start"), slog.Any("error", err))
			StopMainRunLoop()
			return
		}
		slog.Debug("Boot", slog.String("action", "started"), slog.String("state", vm.State().String()))

		// Start the management socket relay so macOS commands can reach the
		// embedded controller inside the VM via vsock port 4000.
		mgmt.StartManagementSocket(name, mgmt.VZConnector{VM: vm, Port: 4000})

		// Port-forwarding listeners — host side. The guest relay is started
		// from crun inside the VM via forward.StartVsock (same as KVM).
		if len(forwards) > 0 {
			ctx, cancel := context.WithCancel(context.Background())
			stop, err := forward.Start(ctx, name, forwards,
				forward.VZTransport{VM: vm})
			if err != nil {
				slog.Warn("Boot", slog.String("action", "start forwards"), slog.Any("error", err))
				cancel()
			} else {
				go func() {
					<-ctx.Done()
					stop()
				}()
				_ = cancel // cleaned up when VM stops
			}
		}
	}()

	// Wait for VM to stop
	go func() {
		err := vm.WaitUntilStopped()
		if err != nil {
			slog.Error("Boot", slog.String("action", "stopped"), slog.Any("error", err))
		} else {
			slog.Debug("Boot", slog.String("action", "stopped"))
		}
		StopMainRunLoop()
	}()

	// Run the main run loop (blocks until StopMainRunLoop)
	RunMainRunLoop()
	return nil
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
