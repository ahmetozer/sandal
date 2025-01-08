package loop

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

type Config struct {
	No   int
	Path string
	File *os.File
	Info *LoopInfo64
}

func (lc Config) Attach(imagePath string) error {

	file, err := os.OpenFile(imagePath, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("failed to open image file: %v", err)
	}
	defer file.Close()

	lc.File, err = os.OpenFile(LOOP_DEVICE_PREFIX+fmt.Sprint(lc.No), os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("could not open loop device: %v", err)
	}

	_, _, errno := unix.Syscall(unix.SYS_IOCTL, lc.File.Fd(), LOOP_SET_FD, file.Fd())
	if errno != 0 {
		return fmt.Errorf("could not associate loop device with file: %v", os.NewSyscallError("ioctl", errno))
	}

	// _, _, errno = unix.Syscall(unix.SYS_IOCTL, lc.File.Fd(), 0x4C00+7, 0)
	// if errno != 0 {
	// 	return fmt.Errorf("could not set autoclear on loop device: %v", os.NewSyscallError("ioctl", errno))
	// }

	if lc.Info != nil {
		_, _, errno = unix.Syscall(unix.SYS_IOCTL, lc.File.Fd(), unix.LOOP_SET_STATUS64, uintptr(unsafe.Pointer(lc.Info)))
		if errno != 0 {
			// Try to clean up if configuration fails
			unix.Syscall(unix.SYS_IOCTL, lc.File.Fd(), unix.LOOP_CLR_FD, 0)
			return fmt.Errorf("failed to set loop device info: %v", errno)
		}
	}

	return nil

}

func (lc Config) Detach() error {
	loopDevice, _ := os.OpenFile(LOOP_DEVICE_PREFIX+fmt.Sprint(lc.No), os.O_RDWR, 0660)
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, loopDevice.Fd(), LOOP_CLR_FD, 0)

	lc.File.Close()

	//0 no error, 6 no device
	if errno != 0 && errno != 6 {
		return fmt.Errorf("could not detach loop device err no %d", errno)
	}
	return nil
}
