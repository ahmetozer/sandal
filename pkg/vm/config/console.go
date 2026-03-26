package config

import "runtime"

// DefaultConsole returns the kernel console parameter for the current platform.
func DefaultConsole() string {
	switch runtime.GOOS {
	case "darwin":
		return "console=hvc0"
	default:
		if runtime.GOARCH == "arm64" {
			return "console=ttyAMA0 earlycon=pl011,mmio,0x09000000"
		}
		return "console=ttyS0 earlycon=uart,io,0x3f8"
	}
}
