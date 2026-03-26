//go:build linux

package guest

import (
	"os"
	"syscall"

	cruntime "github.com/ahmetozer/sandal/pkg/container/runtime"
)

func switchUser(user cruntime.User) error {
	err := switchCredential(user.Credential)
	if err != nil {
		return err
	}

	if user.User == nil {
		return nil
	}

	if user.User.HomeDir != "" {
		// error excepted because if home dir is not availbe
		// expected behavior keep at /
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
