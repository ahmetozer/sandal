//go:build darwin

package env

import "os"

func platformDefaults() (libDir, runDir string) {
	home, _ := os.UserHomeDir()
	return home + "/.sandal/lib", home + "/.sandal/run"
}
