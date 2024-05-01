package container

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

const (
	LOOP_SET_FD        = 0x4C00
	LOOP_CTL_ADD       = 0x4C80
	LOOP_CTL_GET_FREE  = 0x4C82
	LOOP_CTL_REMOVE    = 0x4C01
	LOOP_AUTO_CLEAR    = 0x4C0F
	LOOP_DEVICE_PREFIX = "/dev/loop"
)

// find free loop device
func FindFreeLoopDevice() (int, error) {
	loopControl, err := os.OpenFile(LOOP_DEVICE_PREFIX+"-control", os.O_RDWR, 0660)
	if err != nil {
		return 0, fmt.Errorf("could not open loop control device: %v", err)
	}
	defer loopControl.Close()
	dev, _, errno := unix.Syscall(unix.SYS_IOCTL, loopControl.Fd(), LOOP_CTL_GET_FREE, 0)
	if errno != 0 {
		return 0, fmt.Errorf("could not get free loop device: %v", os.NewSyscallError("ioctl", errno))
	}
	return int(dev), nil
}

func AttachLoopDevice(loopDev int, file *os.File) error {
	loopDevice, err := os.OpenFile(LOOP_DEVICE_PREFIX+fmt.Sprint(loopDev), os.O_RDWR, 0660)

	if err != nil {
		return fmt.Errorf("could not open loop device: %v", err)
	}

	_, _, errno := unix.Syscall(unix.SYS_IOCTL, loopDevice.Fd(), LOOP_SET_FD, file.Fd())
	if errno != 0 {
		return fmt.Errorf("could not associate loop device with file: %v", os.NewSyscallError("ioctl", errno))
	}

	return nil

}
