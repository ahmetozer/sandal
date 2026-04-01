//go:build linux

package env

func platformDefaults() (libDir, runDir string) {
	return "/var/lib/sandal", "/var/run/sandal"
}
