//go:build darwin

package cmd

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/vz"
)

func defaultConsole() string {
	return "console=hvc0"
}

// bootVM boots the VM without applying any cloud-init or initrd overlays.
// Use startVM() for the standard flow with auto-generated overlays.
func bootVM(name string, cfg vmconfig.VMConfig) error {
	// Kill stale VM processes holding the disk file
	if cfg.DiskPath != "" {
		if killed, err := killStaleDiskHolders(cfg.DiskPath); err != nil {
			slog.Warn("bootVM", slog.String("action", "check stale processes"), slog.Any("error", err))
		} else if killed > 0 {
			slog.Info("bootVM", slog.String("action", "killed stale processes"), slog.Int("count", killed), slog.String("disk", cfg.DiskPath))
		}
	}

	// Write PID file
	if err := vmconfig.WritePidFile(name); err != nil {
		slog.Warn("bootVM", slog.String("action", "write pid file"), slog.Any("error", err))
	}
	defer vmconfig.RemovePidFile(name)

	// Set terminal to raw mode for serial console (skip if not a TTY)
	restore, err := setRawTerminal()
	if err != nil {
		slog.Warn("bootVM", slog.String("action", "set raw terminal"), slog.Any("error", err))
		restore = func() {} // no-op
	}
	// Ensure terminal is always restored, even on signals
	defer func() {
		restore()
		exec.Command("stty", "sane").Run()
	}()

	vm, err := vz.NewVM(name, cfg)
	if err != nil {
		restore()
		return fmt.Errorf("creating VM: %w", err)
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
		vz.StopMainRunLoop()
	}()

	// Start VM asynchronously
	go func() {
		if err := vm.Start(); err != nil {
			restore()
			slog.Error("bootVM", slog.String("action", "start"), slog.Any("error", err))
			vz.StopMainRunLoop()
			return
		}
		slog.Debug("bootVM", slog.String("action", "started"), slog.String("state", vm.State().String()))
	}()

	// Wait for VM to stop
	go func() {
		err := vm.WaitUntilStopped()
		if err != nil {
			slog.Error("bootVM", slog.String("action", "stopped"), slog.Any("error", err))
		} else {
			slog.Debug("bootVM", slog.String("action", "stopped"))
		}
		vz.StopMainRunLoop()
	}()

	// Run the main run loop (blocks until StopMainRunLoop)
	vz.RunMainRunLoop()
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

// Terminal raw mode via ioctl (macOS)

type termios struct {
	Iflag  uint64
	Oflag  uint64
	Cflag  uint64
	Lflag  uint64
	Cc     [20]uint8
	Ispeed uint64
	Ospeed uint64
}

func setRawTerminal() (restore func(), err error) {
	fd := os.Stdin.Fd()
	var orig termios
	if err := tcgetattr(fd, &orig); err != nil {
		return nil, fmt.Errorf("tcgetattr: %w", err)
	}

	raw := orig
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Iflag &^= syscall.IXON | syscall.ICRNL | syscall.BRKINT | syscall.INPCK | syscall.ISTRIP
	raw.Oflag &^= syscall.OPOST
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0

	if err := tcsetattr(fd, &raw); err != nil {
		return nil, fmt.Errorf("tcsetattr: %w", err)
	}

	return func() {
		tcsetattr(fd, &orig)
	}, nil
}

func tcgetattr(fd uintptr, t *termios) error {
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		uintptr(syscall.TIOCGETA),
		uintptr(unsafe.Pointer(t)),
	)
	if errno != 0 {
		return errno
	}
	return nil
}

func tcsetattr(fd uintptr, t *termios) error {
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		uintptr(syscall.TIOCSETA),
		uintptr(unsafe.Pointer(t)),
	)
	if errno != 0 {
		return errno
	}
	return nil
}
