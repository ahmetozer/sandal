package loopdev

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

type Config struct {
	No   int
	Path string
	Info *LoopInfo64
}

func (lc Config) Attach(imagePath string) error {

	file, err := os.OpenFile(imagePath, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("failed to open image file: %v", err)
	}
	defer file.Close()

	loopFile, err := os.OpenFile(lc.Path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("could not open loop device: %v", err)
	}
	defer loopFile.Close()

	_, _, errno := unix.Syscall(unix.SYS_IOCTL, loopFile.Fd(), LOOP_SET_FD, file.Fd())
	if errno != 0 {
		return fmt.Errorf("could not associate loop device with file: %v", os.NewSyscallError("ioctl", errno))
	}

	_, _, errno = unix.Syscall(unix.SYS_IOCTL, loopFile.Fd(), 0x4C00+7, 0)
	if errno != 0 {
		unix.Syscall(unix.SYS_IOCTL, loopFile.Fd(), LOOP_CLR_FD, 0)
		return fmt.Errorf("could not set autoclear on loop device: %v", os.NewSyscallError("ioctl", errno))
	}

	if lc.Info != nil {
		_, _, errno = unix.Syscall(unix.SYS_IOCTL, loopFile.Fd(), unix.LOOP_SET_STATUS64, uintptr(unsafe.Pointer(lc.Info)))
		if errno != 0 {
			// Try to clean up if configuration fails
			unix.Syscall(unix.SYS_IOCTL, loopFile.Fd(), unix.LOOP_CLR_FD, 0)
			return fmt.Errorf("failed to set loop device info: %v", errno)
		}
	}

	return nil

}

func (lc Config) Detach() error {
	loopFile, err := os.OpenFile(lc.Path, os.O_RDWR, 0)
	if err != nil {
		// loop device already deleted
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer loopFile.Close()

	_, _, errno := unix.Syscall(unix.SYS_IOCTL, loopFile.Fd(), LOOP_CLR_FD, 0)

	//0 no error, 6 no device
	if errno != 0 && errno != 6 {
		return fmt.Errorf("could not detach loop device err no %d", errno)
	}
	return nil
}
