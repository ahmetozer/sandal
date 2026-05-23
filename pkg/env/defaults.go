package env

import (
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path"
	"strings"
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
	BaseTempDir           string
	BaseImmutableImageDir string
	BaseRootfsDir         string

	DaemonSocket string

	// IsDaemon is true when the current process is running under the sandal daemon.
	IsDaemon bool

	DefaultHostNet string

	// IPv6 dynamic prefix configuration.
	UpstreamInterface string // SANDAL_UPSTREAM_IF — empty triggers auto-detect from default route at daemon start
	IPv6Mode          string // SANDAL_IPV6_MODE — "ndp-proxy" | "pd" | "off"; empty triggers auto-detect via net.DetectIPv6Mode at daemon start
	IPv6PDHint        string // SANDAL_IPV6_PD_HINT — optional prefix length hint for DHCPv6-PD

	Get func(EnvName, DefaultValue string) string

	defaults []SandalSystemEnv

	// System Variables
	PATH string = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	TERM string = "xterm-256color"
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
		BaseTempDir = Get("SANDAL_TEMP_DIR", path.Join(LibDir, "tmp"))

		BaseRootfsDir = Get("SANDAL_ROOTFSDIR", path.Join(RunDir, "rootfs"))
		BaseImmutableImageDir = Get("SANDAL_IMMUTABLEIMAGEDIR", path.Join(RunDir, "immutable"))

		DefaultHostNet = Get("SANDAL_HOST_NET", "172.16.0.1/24,fd34:0135:0123:0:%uv4%::1/120,%uv6%:%uv4%::1/64")

		UpstreamInterface = Get("SANDAL_UPSTREAM_IF", "")
		IPv6Mode = Get("SANDAL_IPV6_MODE", "")
		IPv6PDHint = Get("SANDAL_IPV6_PD_HINT", "")

		DaemonSocket = Get("SANDAL_SOCKET", path.Join(RunDir, "sandal.sock"))

		IsDaemon = os.Getenv("SANDAL_DAEMON_PID") != ""

		Get("SANDAL_LOG_LEVEL", "warn")

		Get = getCurrents
	}
	Get = getMain

	os.Setenv("TERM", Get("TERM", TERM))

	warnIfHostNetIPv6TooBroad(DefaultHostNet)
}

// warnIfHostNetIPv6TooBroad emits a warning for any IPv6 CIDR in
// SANDAL_HOST_NET whose mask is shorter than /64. The renumber path always
// stamps a /64 on the public side using the lower 64 bits as the IID, so a
// broader-than-/64 ULA lets the allocator pick addresses that collapse to the
// same public address after renumber.
func warnIfHostNetIPv6TooBroad(hostNet string) {
	// Treat template tokens as zero hextets purely for mask validation.
	// Neither substitution can change the CIDR mask — only IID/prefix
	// hextets — so this lets the validator run on raw templates without
	// importing pkg/container/net (which would cause a cycle).
	tokenSub := strings.NewReplacer("%uv4%", "0:0", "%uv6%", "0:0:0:0")
	for _, part := range strings.Split(hostNet, ",") {
		trimmed := strings.TrimSpace(part)
		candidate := tokenSub.Replace(trimmed)
		_, ipnet, err := net.ParseCIDR(candidate)
		if err != nil {
			continue
		}
		if ipnet.IP.To4() != nil {
			continue
		}
		ones, _ := ipnet.Mask.Size()
		if ones < 64 {
			slog.Warn("SANDAL_HOST_NET: IPv6 mask shorter than /64 may cause public address collisions after renumber",
				"cidr", trimmed, "mask", ones)
		}
	}
}
