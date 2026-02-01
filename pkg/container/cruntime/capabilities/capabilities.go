package capabilities

import (
	"github.com/ahmetozer/sandal/pkg/container/config/wrapper"
	"golang.org/x/sys/unix"
)

type Capabilities struct {
	AddCapabilities  wrapper.StringFlags
	DropCapabilities wrapper.StringFlags
	Privileged       bool
}

type Name string

// capabilityMap maps capability names to their numeric values
var capabilityMap = map[string]uint32{
	"CHOWN":            unix.CAP_CHOWN,
	"DAC_OVERRIDE":     unix.CAP_DAC_OVERRIDE,
	"DAC_READ_SEARCH":  unix.CAP_DAC_READ_SEARCH,
	"FOWNER":           unix.CAP_FOWNER,
	"FSETID":           unix.CAP_FSETID,
	"KILL":             unix.CAP_KILL,
	"SETGID":           unix.CAP_SETGID,
	"SETUID":           unix.CAP_SETUID,
	"SETPCAP":          unix.CAP_SETPCAP,
	"LINUX_IMMUTABLE":  unix.CAP_LINUX_IMMUTABLE,
	"NET_BIND_SERVICE": unix.CAP_NET_BIND_SERVICE,
	"NET_BROADCAST":    unix.CAP_NET_BROADCAST,
	"NET_ADMIN":        unix.CAP_NET_ADMIN,
	"NET_RAW":          unix.CAP_NET_RAW,
	"IPC_LOCK":         unix.CAP_IPC_LOCK,
	"IPC_OWNER":        unix.CAP_IPC_OWNER,
	"SYS_MODULE":       unix.CAP_SYS_MODULE,
	"SYS_RAWIO":        unix.CAP_SYS_RAWIO,
	"SYS_CHROOT":       unix.CAP_SYS_CHROOT,
	"SYS_PTRACE":       unix.CAP_SYS_PTRACE,
	"SYS_PACCT":        unix.CAP_SYS_PACCT,
	"SYS_ADMIN":        unix.CAP_SYS_ADMIN,
	"SYS_BOOT":         unix.CAP_SYS_BOOT,
	"SYS_NICE":         unix.CAP_SYS_NICE,
	"SYS_RESOURCE":     unix.CAP_SYS_RESOURCE,
	"SYS_TIME":         unix.CAP_SYS_TIME,
	"SYS_TTY_CONFIG":   unix.CAP_SYS_TTY_CONFIG,
	"MKNOD":            unix.CAP_MKNOD,
	"LEASE":            unix.CAP_LEASE,
	"AUDIT_WRITE":      unix.CAP_AUDIT_WRITE,
	"AUDIT_CONTROL":    unix.CAP_AUDIT_CONTROL,
	"SETFCAP":          unix.CAP_SETFCAP,
	"MAC_OVERRIDE":     unix.CAP_MAC_OVERRIDE,
	"MAC_ADMIN":        unix.CAP_MAC_ADMIN,
	"SYSLOG":           unix.CAP_SYSLOG,
	"WAKE_ALARM":       unix.CAP_WAKE_ALARM,
	"BLOCK_SUSPEND":    unix.CAP_BLOCK_SUSPEND,
	"AUDIT_READ":       unix.CAP_AUDIT_READ,
}

// Start with a default set of capabilities for non-privileged containers
// These are common safe capabilities that containers typically need
var defaultCaps = []string{
	"AUDIT_WRITE",      // Write records to kernel auditing log.
	"CHOWN",            // Make arbitrary changes to file UIDs and GIDs (see chown(2)).
	"DAC_OVERRIDE",     // Bypass file read, write, and execute permission checks.
	"FOWNER",           // Bypass permission checks on operations that normally require the file system UID of the process to match the UID of the file.
	"FSETID",           // Don't clear set-user-ID and set-group-ID permission bits when a file is modified.
	"KILL",             // Bypass permission checks for sending signals.
	"MKNOD",            // Create special files using mknod(2).
	"NET_BIND_SERVICE", // Bind a socket to internet domain privileged ports (port numbers less than 1024).
	"NET_RAW",          // Use RAW and PACKET sockets.
	"SETFCAP",          // Set file capabilities.
	"SETGID",           // Make arbitrary manipulations of process GIDs and supplementary GID list.
	"SETPCAP",          // Modify process capabilities.
	"SETUID",           // Make arbitrary manipulations of process UIDs.
	"SYS_CHROOT",       // Use chroot(2), change root directory.
}
