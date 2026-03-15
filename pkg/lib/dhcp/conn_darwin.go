//go:build darwin

package dhcp

import (
	"net"
	"syscall"
)

// ipBoundIF is the macOS-specific socket option that binds a socket
// to a particular interface by index (equivalent to Linux SO_BINDTODEVICE).
const ipBoundIF = 25 // IP_BOUND_IF

func setSockOpts(fd uintptr, iface *net.Interface) error {
	if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1); err != nil {
		return err
	}
	if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
		return err
	}
	if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1); err != nil {
		return err
	}
	return syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, ipBoundIF, iface.Index)
}
