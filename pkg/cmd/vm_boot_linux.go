//go:build linux

package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"unsafe"

	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/kvm"
)

func defaultConsole() string {
	// PL011 UART is the primary console on ARM64 (built-in driver, probes early).
	// virtio-console would be better but virtio_mmio is a kernel module on Alpine,
	// so it can't be ready before init starts.
	if runtime.GOARCH == "arm64" {
		return "console=ttyAMA0 earlycon=pl011,mmio,0x09000000"
	}
	return "console=ttyS0 earlycon=uart,io,0x3f8"
}

// bootVM boots the VM using KVM.
// Use startVM() for the standard flow with auto-generated overlays.
func bootVM(name string, cfg vmconfig.VMConfig) error {
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
	defer func() {
		restore()
		exec.Command("stty", "sane").Run()
	}()

	vm, err := kvm.NewVM(name, cfg)
	if err != nil {
		restore()
		return fmt.Errorf("creating VM: %w", err)
	}
	defer vm.Close()

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
	}()

	// Start VM
	if err := vm.Start(); err != nil {
		restore()
		return fmt.Errorf("starting VM: %w", err)
	}
	slog.Debug("bootVM", slog.String("action", "started"), slog.String("state", vm.State().String()))

	// Wait for VM to stop
	if err := vm.WaitUntilStopped(); err != nil {
		slog.Error("bootVM", slog.String("action", "stopped"), slog.Any("error", err))
	} else {
		slog.Debug("bootVM", slog.String("action", "stopped"))
	}

	return nil
}

// Terminal raw mode via ioctl (Linux)

const (
	tcgets = 0x5401
	tcsets = 0x5402
)

type termios struct {
	Iflag  uint32
	Oflag  uint32
	Cflag  uint32
	Lflag  uint32
	Line   uint8
	Cc     [19]uint8
	Ispeed uint32
	Ospeed uint32
}

func setRawTerminal() (restore func(), err error) {
	fd := os.Stdin.Fd()
	var orig termios
	if err := tcgetattr(fd, &orig); err != nil {
		return nil, fmt.Errorf("tcgetattr: %w", err)
	}

	raw := orig
	// Input flags: disable IGNBRK, BRKINT, PARMRK, ISTRIP, INLCR, IGNCR, ICRNL, IXON
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	// Output flags: disable OPOST
	raw.Oflag &^= syscall.OPOST
	// Local flags: disable ECHO, ECHONL, ICANON, ISIG, IEXTEN
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	// Control flags: disable CSIZE, PARENB; set CS8
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	// Control chars
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
		uintptr(tcgets),
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
		uintptr(tcsets),
		uintptr(unsafe.Pointer(t)),
	)
	if errno != 0 {
		return errno
	}
	return nil
}
