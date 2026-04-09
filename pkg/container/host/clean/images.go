//go:build linux || darwin

package clean

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ahmetozer/sandal/pkg/env"
)

// PlanImages returns Actions for every .sqfs file in env.BaseImageDir
// that is not referenced by any container in the usage set.
func PlanImages(usage UsageSet) []Action {
	dir := env.BaseImageDir
	if ok, _ := IsInsideLibDir(dir); !ok {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var actions []Action
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".sqfs") {
			continue
		}
		full := filepath.Join(dir, name)
		if _, used := usage.Images[full]; used {
			continue
		}
		if ok, _ := IsInsideLibDir(full); !ok {
			continue
		}
		actions = append(actions, Action{
			Path:   full,
			Kind:   KindImage,
			Reason: "not referenced by any container",
			Bytes:  fileSize(full),
		})
	}
	return actions
}
