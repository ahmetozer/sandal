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

	BaseUpperdir         string
	BaseWorkdir          string
	BaseSquashFSMountDir string
	BaseRootfsDir        string

	DaemonSocket string

	DefaultHostNet string

	Get func(EnvName, DefaultValue string) string

	defaults []sEnv
)

func GetDefaults() []string {
	var cur []string
	for _, d := range defaults {
		cur = append(cur, d.name+"="+d.def)
	}
	return cur
}

func GetCurrents() []string {
	var cur []string
	for _, d := range defaults {
		env := Get(d.name, "")
		if env == "" {
			env = "\t\t(not set but used as: " + d.cur + ")"
		}
		cur = append(cur, d.name+"="+env)
	}
	return cur
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
		BaseUpperdir = Get("SANDAL_UPPERDIR", path.Join(LibDir, "upper"))

		BaseWorkdir = Get("SANDAL_WORKDIR", path.Join(RunDir, "workdir"))
		BaseRootfsDir = Get("SANDAL_ROOTFSDIR", path.Join(RunDir, "rootfs"))
		BaseSquashFSMountDir = Get("SANDAL_SQUASHFSMOUNTDIR", path.Join(RunDir, "squashfs"))

		DefaultHostNet = Get("SANDAL_HOST_NET", "172.16.0.1/24;fd34:0135:0123::1/64")

		DaemonSocket = Get("SANDAL_SOCKET", path.Join(LibDir, "sandal.sock"))
		Get = getCurrents
	}
	Get = getMain

	if len(os.Args) > 1 {
		IsDaemon = os.Args[1] == "daemon"
	}

}
