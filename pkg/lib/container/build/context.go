package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BuildContext is the rooted directory tree that COPY/ADD reads from.
// It enforces ignore-file exclusion and prevents path escape outside
// the root via symlinks or "..".
type BuildContext struct {
	Root    string         // absolute path on host
	Ignore  *IgnoreMatcher // may be nil
}

// NewBuildContext opens the given directory as a build context. It loads
// `.dockerignore` from the root if present.
func NewBuildContext(dir string) (*BuildContext, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving context dir: %w", err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat context dir: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("build context %s is not a directory", abs)
	}
	bc := &BuildContext{Root: abs}

	ignoreFile := filepath.Join(abs, ".dockerignore")
	if f, err := os.Open(ignoreFile); err == nil {
		defer f.Close()
		m, err := LoadIgnore(f)
		if err != nil {
			return nil, fmt.Errorf("parsing .dockerignore: %w", err)
		}
		bc.Ignore = m
	}
	return bc, nil
}

// Resolve takes a relative source path and returns the absolute on-disk
// path under the context root. It rejects paths that escape the root
// via "..", absolute paths, or symlinks pointing outside.
func (bc *BuildContext) Resolve(rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("COPY source %q must be relative to build context", rel)
	}
	abs := filepath.Join(bc.Root, rel)
	// EvalSymlinks resolves symlinks; if path doesn't exist it returns error.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// Fall back to lexical check (for non-existent paths during dry runs).
		if !strings.HasPrefix(abs+string(filepath.Separator), bc.Root+string(filepath.Separator)) && abs != bc.Root {
			return "", fmt.Errorf("COPY source %q escapes build context", rel)
		}
		return abs, nil
	}
	if !strings.HasPrefix(resolved+string(filepath.Separator), bc.Root+string(filepath.Separator)) && resolved != bc.Root {
		return "", fmt.Errorf("COPY source %q resolves outside build context (via symlink)", rel)
	}
	return resolved, nil
}

// IsExcluded returns true if rel (relative path under Root) is excluded.
func (bc *BuildContext) IsExcluded(rel string) bool {
	if bc.Ignore == nil {
		return false
	}
	return bc.Ignore.Excluded(rel)
}
