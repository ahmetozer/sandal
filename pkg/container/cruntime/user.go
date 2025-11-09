package cruntime

import (
	"os"
	os_user "os/user"
	"strconv"
	"strings"
	"syscall"
)

func switchUser(user string) (err error) {

	if user == "" {
		return nil
	}

	var (
		uid int
		gid int
	)
	credential := strings.Split(user, ":")

	if uid, err = strconv.Atoi(credential[0]); err != nil {
		user, err := os_user.Lookup(credential[0])
		if err == nil {
			uid, _ = strconv.Atoi(user.Uid)
			// if usergroup is not presented, try gid info from user information

			if len(credential) == 1 && user.Gid != "" {
				credential = append(credential, user.Gid)
			}
		} else {
			return err
		}

		if user.HomeDir != "" {
			os.Chdir(user.HomeDir)
		}
	}

	// if usergroup is not presented, set group name identical to username
	if len(credential) == 1 {
		credential = append(credential, credential[0])
	}

	if gid, err = strconv.Atoi(credential[1]); err != nil {
		group, err := os_user.LookupGroup(credential[1])
		if err == nil {
			gid, _ = strconv.Atoi(group.Gid)
		}
	}

	err = syscall.Setgid(gid)
	if err != nil {
		return err
	}
	err = syscall.Setuid(uid)

	return
}
