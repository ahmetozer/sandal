package env

import (
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
)

var (
	// Main folder for all container related files

	BinLoc string

	LibDir string
	RunDir string

	BaseImageDir string
	BaseStateDir string

	BaseChangeDir         string
	BaseSnapshotDir       string
	BaseKernelDir         string
	BaseImmutableImageDir string
	BaseRootfsDir         string

	DaemonSocket string

	// IsDaemon is true when the current process is running under the sandal daemon.
	IsDaemon bool

	DefaultHostNet string

	VMBinPath string

	Get func(EnvName, DefaultValue string) string

	defaults []SandalSystemEnv
)

func GetDefaults() []SandalSystemEnv {
	return defaults
}

func init() {
	if os.Getpid() == 1 {
		// Running as VM init (PID 1): /proc may not be fully ready,
		// PATH is empty, and LookPath can block. Use argv[0] directly.
		if len(os.Args) > 0 {
			BinLoc = os.Args[0]
		} else {
			BinLoc = "/init"
		}
	} else if len(os.Args) > 0 {
		ex, err := os.Executable()
		if err != nil {
			BinLoc = os.Args[0]
		} else {
			BinLoc, err = exec.LookPath(ex)
			if err != nil {
				slog.Debug(err.Error())
				BinLoc = os.Args[0]
			}
		}
	} else {
		BinLoc = "/proc/self/exe"
	}

	Get = getInit
	defaultLibDir, defaultRunDir := platformDefaults()
	for i := 0; i < 2; i++ {
		LibDir = Get("SANDAL_LIB_DIR", defaultLibDir)
		RunDir = Get("SANDAL_RUN_DIR", defaultRunDir)

		BaseImageDir = Get("SANDAL_IMAGE_DIR", path.Join(LibDir, "image"))
		BaseStateDir = Get("SANDAL_STATE_DIR", path.Join(LibDir, "state"))
		BaseChangeDir = Get("SANDAL_CHANGE_DIR", path.Join(LibDir, "changedir"))
		BaseSnapshotDir = Get("SANDAL_SNAPSHOT_DIR", path.Join(LibDir, "snapshot"))
		BaseKernelDir = Get("SANDAL_KERNEL_DIR", path.Join(LibDir, "kernel"))

		BaseRootfsDir = Get("SANDAL_ROOTFSDIR", path.Join(RunDir, "rootfs"))
		BaseImmutableImageDir = Get("SANDAL_IMMUTABLEIMAGEDIR", path.Join(RunDir, "immutable"))

		DefaultHostNet = Get("SANDAL_HOST_NET", "172.16.0.1/24,fd34:0135:0123::1/64")

		DaemonSocket = Get("SANDAL_SOCKET", path.Join(RunDir, "sandal.sock"))

		IsDaemon = os.Getenv("SANDAL_DAEMON_PID") != ""

		VMBinPath = Get("SANDAL_VM_BIN", filepath.Join(LibDir, "bin", "sandal"))

		Get("SANDAL_LOG_LEVEL", "warn")

		Get = getCurrents
	}
	Get = getMain

}
