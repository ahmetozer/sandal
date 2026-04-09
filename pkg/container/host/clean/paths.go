//go:build linux || darwin

package clean

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ahmetozer/sandal/pkg/env"
)

// IsInsideLibDir reports whether target resolves to a path strictly
// inside env.LibDir. It resolves symlinks on both sides so a symlink
// planted in LibDir cannot smuggle the cleanup routine into a path
// outside LibDir. A target equal to LibDir itself is rejected — we
// never delete the root directory.
func IsInsideLibDir(target string) (bool, error) {
	return isInside(target, env.LibDir)
}

// IsInsideSandalArea reports whether target is inside LibDir OR
// RunDir. Rootfs mount points live under RunDir, so deletions that
// touch them also need to be validated against that root.
func IsInsideSandalArea(target string) (bool, error) {
	if ok, err := isInside(target, env.LibDir); ok || err != nil {
		return ok, err
	}
	if env.RunDir == "" {
		return false, nil
	}
	return isInside(target, env.RunDir)
}

func isInside(target, root string) (bool, error) {
	if target == "" {
		return false, nil
	}
	if root == "" {
		return false, fmt.Errorf("sandal root dir is empty")
	}

	rootResolved, err := resolveAbs(root)
	if err != nil {
		return false, fmt.Errorf("resolving root: %w", err)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false, nil
	}
	targetResolved, err := resolveAbs(absTarget)
	if err != nil {
		// The target (or a descendant) does not exist — common for
		// post-removal checks and for the "{ChangeDir}/gone.img"
		// case. Walk up to the nearest existing ancestor, resolve
		// symlinks on it, and rejoin the remaining components. This
		// is critical on hosts where e.g. /var/run is a symlink to
		// /run: if we skipped this step we'd compare a lexical
		// /var/run/sandal/... against a resolved /run/sandal/...
		// and reject a legitimate path.
		ancestor := absTarget
		var tail []string
		for {
			parent := filepath.Dir(ancestor)
			if parent == ancestor {
				// Reached root without finding anything that
				// exists — fall back to lexical comparison.
				targetResolved = filepath.Clean(absTarget)
				break
			}
			if resolved, rerr := resolveAbs(parent); rerr == nil {
				tail = append([]string{filepath.Base(ancestor)}, tail...)
				targetResolved = filepath.Join(append([]string{resolved}, tail...)...)
				break
			}
			tail = append([]string{filepath.Base(ancestor)}, tail...)
			ancestor = parent
		}
	}
	if targetResolved == rootResolved {
		return false, nil
	}
	// Append the separator so "/var/lib/sandal-other" is not
	// considered inside "/var/lib/sandal".
	prefix := rootResolved + string(os.PathSeparator)
	return strings.HasPrefix(targetResolved, prefix), nil
}

// resolveAbs returns an absolute, symlink-resolved form of p.
func resolveAbs(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return resolved, nil
}
