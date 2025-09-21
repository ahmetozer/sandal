package namespace

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func (NS Namespaces) Unshare() (err error) {
	if err = unix.Unshare(int(NS.Cloneflags())); err != nil {
		err = fmt.Errorf("unshare namespaces: %v", err)
	}
	return
}
