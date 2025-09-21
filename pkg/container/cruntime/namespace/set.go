package namespace

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func (NS Namespaces) SetNS() (err error) {
	var files = make(map[Name]*os.File)

	for nsName, nsConf := range NS {
		if nsConf.IsHost {
			continue
		}
		if nsConf.IsUserDefined {
			userNameSpaceInfo := strings.Split(nsConf.String(), ":")
			switch {
			// Default is "proccessPidNumber"
			case len(userNameSpaceInfo) == 1: // Example: "2739" which is PID number
				pid, _ := strconv.Atoi(userNameSpaceInfo[0])
				files[nsName], err = getFileByPath(getPathByPid(nsName, pid))
				if err != nil {
					return
				}
				defer files[nsName].Close()
			case len(userNameSpaceInfo) == 2 && userNameSpaceInfo[0] == "pid": // Example: "pid:2739"
				pid, _ := strconv.Atoi(userNameSpaceInfo[1])
				files[nsName], err = getFileByPath(getPathByPid(nsName, pid))
				if err != nil {
					return
				}
				defer files[nsName].Close()
			case len(userNameSpaceInfo) == 2 && userNameSpaceInfo[0] == "file": // Example: "file:/var/run/netns/mynamespace"
				files[nsName], err = getFileByPath(userNameSpaceInfo[1])
				if err != nil {
					return
				}
				defer files[nsName].Close()
			default:
				return fmt.Errorf("unknown namespace information: %s", nsConf.String())
			}
		}
	}

	for nsName, nsConf := range NS {
		if nsConf.IsUserDefined {
			err = setNsByFile(files[nsName], nsName)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func getPathByPid(nsName Name, pid int) string {
	return fmt.Sprintf("/proc/%d/ns/%s", pid, nsName)

}

// func setNsByPid(nsName Name, pid int) error {
// 	return setNsByPath(getPathByPid(nsName, pid), nsName)
// }

func getFileByPath(path string) (file *os.File, err error) {
	file, err = os.Open(path)
	if err != nil {
		err = fmt.Errorf("failed to open namespace file %s: %v", path, err)
	}
	return
}

// func setNsByPath(path string, nsName Name) error {
// 	file, err := getFileByPath(path)
// 	if err != nil {
// 		return err
// 	}
// 	defer file.Close()
// 	return setNsByFile(file, nsName)
// }

func setNsByFile(file *os.File, nsName Name) error {
	if file == nil {
		return fmt.Errorf("namespace file is nil")
	}
	return setNsByInt(int(file.Fd()), nsName)
}

func setNsByInt(reference int, nsName Name) error {
	nstype, b := namespaceList[nsName]
	if !b {
		return fmt.Errorf("failed find namespace clone flag: %s", nsName)
	}
	// Set the namespace
	if err := unix.Setns(reference, int(nstype)); err != nil {
		return fmt.Errorf("failed to set namespace %s: %v", nsName, err)
	}
	return nil
}

func (source Namespaces) SetEmptyToPid(pid int) (new Namespaces) {

	new = make(Namespaces, len(source))

	for name, conf := range source {
		old := source[name]
		new[name] = old
		if conf.IsUserDefined {
			continue
		}
		if conf.IsHost {
			continue
		}
		// It looks like this is being created and is just failing to set. It's hard to find documentation that explicitly says this, but by reading kernel source code, it looks like as with user namespaces, you can't set time namespaces on a multithreaded process, but as with PID namespaces, if you spawn a child process, it should have it set.
		if name == "time" {
			continue
		}

		*old.UserValue = fmt.Sprintf("pid:%d", pid)
		old.IsUserDefined = true

		new[name] = old

	}
	return
}
