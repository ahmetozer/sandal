package loopdev

import (
	"fmt"
	"os"
	"strconv"

	"github.com/ahmetozer/sandal/pkg/env"
	"golang.org/x/sys/unix"
)

const (
	// https://github.com/util-linux/util-linux/blob/master/include/loopdev.h#L46
	LOOP_SET_FD       = 0x4C00
	LOOP_CLR_FD       = 0x4C01
	LOOP_SET_STATUS64 = 0x4C04
	LOOP_CTL_ADD      = 0x4C80
	LOOP_CTL_REMOVE   = 0x4C81
	LOOP_CTL_GET_FREE = 0x4C82
)

var (
	LOOP_DEVICE_PREFIX = env.Get("LOOP_DEVICE_PREFIX", "/dev/loop")
	LOOP_CONROL_PREFIX = LOOP_DEVICE_PREFIX + "-control"
)

// find free loop device
func FindFreeLoopDevice() (Config, error) {

	c := Config{}
	loopControl, err := os.OpenFile(LOOP_CONROL_PREFIX, os.O_RDWR, 0)
	if err != nil {
		return c, fmt.Errorf("could not open loop control device: %v", err)
	}
	defer loopControl.Close()
	dev, _, errno := unix.Syscall(unix.SYS_IOCTL, loopControl.Fd(), LOOP_CTL_GET_FREE, 0)
	if errno != 0 {
		return c, fmt.Errorf("could not get free loop device: %v", os.NewSyscallError("ioctl", errno))
	}

	c.No = int(dev)
	c.Path = LOOP_DEVICE_PREFIX + strconv.Itoa(c.No)
	return c, nil
}
