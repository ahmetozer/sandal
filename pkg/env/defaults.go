package env

import (
	"os"
	"path"
)

var (
	// Main folder for all container related files
	IsDaemon bool

	BinLoc string

	LibDir string
	RunDir string

	BaseImageDir string
	BaseStateDir string

	BaseChangeDir        string
	BaseSquashFSMountDir string
	BaseRootfsDir        string

	DaemonSocket string

	DefaultHostNet string

	Get func(EnvName, DefaultValue string) string

	defaults []SandalSystemEnv
)

func GetDefaults() []SandalSystemEnv {
	return defaults
}

func init() {

	if len(os.Args) > 0 {
		BinLoc = os.Args[0]
	} else {
		BinLoc = "/proc/self/exe"
	}

	Get = getInit
	for i := 0; i < 2; i++ {
		LibDir = Get("SANDAL_LIB_DIR", "/var/lib/sandal")
		RunDir = Get("SANDAL_RUN_DIR", "/var/run/sandal")

		BaseImageDir = Get("SANDAL_IMAGE_DIR", path.Join(LibDir, "image"))
		BaseStateDir = Get("SANDAL_STATE_DIR", path.Join(LibDir, "state"))
		BaseChangeDir = Get("SANDAL_CHANGE_DIR", path.Join(LibDir, "changedir"))

		BaseRootfsDir = Get("SANDAL_ROOTFSDIR", path.Join(RunDir, "rootfs"))
		BaseSquashFSMountDir = Get("SANDAL_SQUASHFSMOUNTDIR", path.Join(RunDir, "squashfs"))

		DefaultHostNet = Get("SANDAL_HOST_NET", "172.16.0.1/24;fd34:0135:0123::1/64")

		DaemonSocket = Get("SANDAL_SOCKET", path.Join(RunDir, "sandal.sock"))

		Get("SANDAL_LOG_LEVEL", "info")

		Get = getCurrents
	}
	Get = getMain

	if len(os.Args) > 1 {
		IsDaemon = os.Args[1] == "daemon"
	}

}
