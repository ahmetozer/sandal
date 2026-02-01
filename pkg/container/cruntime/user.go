package cruntime

import (
	"os"
	os_user "os/user"
	"strconv"
	"strings"
	"syscall"
)

type User struct {
	Credential *syscall.Credential
	User       *os_user.User
}

func getUser(ug string) (User User, err error) {
	if ug == "" {
		// set default user to root
		User.User = &os_user.User{
			Uid:      "0",
			Gid:      "0",
			Username: "root",
			Name:     "root",
			HomeDir:  "/root",
		}
		User.Credential = &syscall.Credential{
			Uid: 0,
			Gid: 0,
		}
		return
	}

	var (
		uid uint64
		gid uint64
	)
	user_group := strings.Split(ug, ":")

	// if it's user id instead of username set username, else get user id from username
	if uid, err = strconv.ParseUint(user_group[0], 10, 32); err != nil {
		User.User, err = os_user.Lookup(user_group[0])
		if err == nil {
			uid, _ = strconv.ParseUint(User.User.Uid, 10, 32)
			// if usergroup is not presented, try gid info from user information

			if len(user_group) == 1 && User.User.Gid != "" {
				user_group = append(user_group, User.User.Gid)
			}
		} else {
			return
		}

	}

	// if usergroup is not presented, set group name identical to username
	if len(user_group) == 1 {
		user_group = append(user_group, user_group[0])
	}

	// if it's group id instead of groupname set groupid, else get user id from groupname
	if gid, err = strconv.ParseUint(user_group[1], 10, 32); err != nil {
		group, err := os_user.LookupGroup(user_group[1])
		if err == nil {
			gid, _ = strconv.ParseUint(group.Gid, 10, 32)
		}
	}

	User.Credential = &syscall.Credential{}
	User.Credential.Uid = uint32(uid)
	User.Credential.Gid = uint32(gid)

	return
}

func switchUser(user User) error {
	err := switchCredential(user.Credential)
	if err != nil {
		return err
	}

	if user.User == nil {
		return nil
	}

	if user.User.HomeDir != "" {
		// error excepted because if home dir is not availbe
		// expected behivor keep at /
		os.Chdir(user.User.HomeDir)
	}

	return nil
}

func switchCredential(Credential *syscall.Credential) (err error) {
	if Credential == nil {
		return
	}

	// Get current UID and GID to check if we're already running as the target user
	currentUid := uint32(os.Getuid())
	currentGid := uint32(os.Getgid())

	// Only call setgid if we're actually changing the GID
	// This is critical for root: calling setresgid(0,0,0) when already GID 0
	// can trigger capability changes
	if currentGid != Credential.Gid {
		// Use setresgid instead of setgid to preserve capabilities better
		err = syscall.Setresgid(int(Credential.Gid), int(Credential.Gid), int(Credential.Gid))
		if err != nil {
			return
		}
	}

	// Only call setuid if we're actually changing the UID
	// This is critical for root: calling setresuid(0,0,0) when already UID 0
	// causes the kernel to restore full capabilities, bypassing dropped capabilities
	if currentUid != Credential.Uid {
		// If we're switching from root (UID 0) to a non-root user,
		// we need to set KEEPCAPS so capabilities aren't dropped during the UID change
		if currentUid == 0 && Credential.Uid != 0 {
			// PR_SET_KEEPCAPS: Keep capabilities when switching from root to non-root
			if _, _, errno := syscall.Syscall(syscall.SYS_PRCTL, syscall.PR_SET_KEEPCAPS, 1, 0); errno != 0 {
				return errno
			}
		}

		// Use setresuid instead of setuid to preserve capabilities better
		// Setting all three (real, effective, saved) to the same value
		err = syscall.Setresuid(int(Credential.Uid), int(Credential.Uid), int(Credential.Uid))
		if err != nil {
			return
		}

		// After switching from root to non-root, capabilities are moved to permitted set
		// but cleared from effective set. We need to restore them to effective set.
		if currentUid == 0 && Credential.Uid != 0 {
			// Clear the KEEPCAPS flag as it's no longer needed
			if _, _, errno := syscall.Syscall(syscall.SYS_PRCTL, syscall.PR_SET_KEEPCAPS, 0, 0); errno != 0 {
				return errno
			}
		}
	}

	return
}
