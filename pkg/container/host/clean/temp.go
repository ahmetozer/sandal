//go:build linux || darwin

package clean

import (
	"os"

	"github.com/ahmetozer/sandal/pkg/env"
)

// PlanTemp returns actions to remove all entries inside BaseTempDir.
// These are leftover temp files from interrupted pulls/builds.
func PlanTemp() []Action {
	if env.BaseTempDir == "" {
		return nil
	}

	entries, err := os.ReadDir(env.BaseTempDir)
	if err != nil {
		return nil
	}

	var actions []Action
	for _, e := range entries {
		p := env.BaseTempDir + "/" + e.Name()
		actions = append(actions, Action{
			Path:   p,
			Kind:   KindTemp,
			Reason: "leftover temp file",
			Bytes:  fileSize(p),
		})
	}
	return actions
}
