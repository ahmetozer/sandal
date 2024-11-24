package config

import (
	"os"
	"path"
)

func GetEnv(EnvName, DefaultValue string) string {
	env := os.Getenv(EnvName)
	if env == "" {
		return DefaultValue
	}
	return env
}

var (
	// Main folder for all container related files
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

	isDeamon bool
)

func init() {

	LibDir = GetEnv("SANDAL_LIB_DIR", "/var/lib/sandal")
	RunDir = GetEnv("SANDAL_RUN_DIR", "/var/run/sandal")

	BaseImageDir = GetEnv("SANDAL_IMAGE_DIR", path.Join(LibDir, "image"))
	BaseStateDir = GetEnv("SANDAL_STATE_DIR", path.Join(LibDir, "state"))
	BaseUpperdir = GetEnv("SANDAL_UPPERDIR", path.Join(LibDir, "upper"))

	BaseWorkdir = GetEnv("SANDAL_WORKDIR", path.Join(RunDir, "workdir"))
	BaseRootfsDir = GetEnv("SANDAL_ROOTFSDIR", path.Join(RunDir, "rootfs"))
	BaseSquashFSMountDir = GetEnv("SANDAL_SQUASHFSMOUNTDIR", path.Join(RunDir, "squashfs"))

	DaemonSocket = GetEnv("DAEMON_SOCKET", path.Join(RunDir, "sandal.sock"))

	DefaultHostNet = GetEnv("SANDAL_HOST_NET", "172.16.0.1/24;fd34:0135:0123::1/64")
}

func SetModeDeamon() {
	isDeamon = true
}
