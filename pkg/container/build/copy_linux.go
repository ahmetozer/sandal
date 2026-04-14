//go:build linux

// Package build implements the runtime side of `sandal build` on Linux:
// driving stages, executing RUN, and copying files for COPY/ADD.
package build

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CopyParams configures one COPY/ADD instruction execution.
type CopyParams struct {
	// SrcRoot is the on-disk root from which sources are resolved.
	// For COPY from build context: the context root.
	// For COPY --from=<stage>: the merged rootfs of that stage.
	SrcRoot string
	// SrcPaths are the per-source paths relative to SrcRoot.
	SrcPaths []string
	// Dst is the destination path inside the container rootfs.
	// Trailing "/" forces directory semantics (multiple sources require this).
	Dst string
	// DstRoot is the on-disk root that represents the container rootfs
	// (i.e. the upper-dir / change-dir we are writing into).
	DstRoot string
	// Excluded reports whether a relative path under SrcRoot is excluded
	// (.dockerignore filter). May be nil.
	Excluded func(rel string) bool
}

// Apply executes a COPY/ADD: copies each SrcPath under SrcRoot into the
// resolved Dst path under DstRoot, honouring directory vs file semantics
// and the Excluded filter.
//
// File ownership and permissions are preserved from the source.
// Symlinks are copied as symlinks (not dereferenced).
// Devices/fifos/sockets are silently skipped (not valid in build context).
func Apply(p CopyParams) error {
	if len(p.SrcPaths) == 0 {
		return fmt.Errorf("COPY/ADD requires at least one source")
	}
	if p.Dst == "" {
		return fmt.Errorf("COPY/ADD requires a destination")
	}

	dstIsDir := strings.HasSuffix(p.Dst, "/") || len(p.SrcPaths) > 1
	dstAbs := filepath.Join(p.DstRoot, p.Dst)

	if dstIsDir {
		if err := os.MkdirAll(dstAbs, 0755); err != nil {
			return fmt.Errorf("creating destination dir %s: %w", dstAbs, err)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(dstAbs), 0755); err != nil {
			return fmt.Errorf("creating destination parent %s: %w", filepath.Dir(dstAbs), err)
		}
	}

	for _, src := range p.SrcPaths {
		srcAbs, err := safeJoin(p.SrcRoot, src)
		if err != nil {
			return err
		}
		st, err := os.Lstat(srcAbs)
		if err != nil {
			return fmt.Errorf("stat source %s: %w", src, err)
		}

		if st.IsDir() {
			// Docker semantics: COPY of a directory always copies its
			// CONTENTS into the destination — never the directory itself
			// as a subdirectory, regardless of whether the dst already
			// exists or has a trailing slash. With multiple sources, each
			// source's contents are merged under dstAbs.
			if err := copyTree(srcAbs, dstAbs, p.SrcRoot, p.Excluded); err != nil {
				return err
			}
			continue
		}

		// Non-directory source: destination rules differ.
		//   - Dst ends in "/" or multiple sources → dst is a directory,
		//     place the source basename under it.
		//   - Dst exists as a directory → same (place basename under).
		//   - Otherwise → copy to dst as a file (rename semantics).
		var perSrcDst string
		if dstIsDir {
			perSrcDst = filepath.Join(dstAbs, filepath.Base(srcAbs))
		} else if dstInfo, err := os.Lstat(dstAbs); err == nil && dstInfo.IsDir() {
			perSrcDst = filepath.Join(dstAbs, filepath.Base(srcAbs))
		} else {
			perSrcDst = dstAbs
		}
		if err := copyOne(srcAbs, perSrcDst, st); err != nil {
			return err
		}
	}
	return nil
}

// safeJoin joins root and rel, refusing the result if it escapes root.
// Symlinks within root are NOT resolved here; callers that need to follow
// symlinks should do so with their own EvalSymlinks + recheck.
func safeJoin(root, rel string) (string, error) {
	cleaned := filepath.Clean("/" + rel)            // normalises "..", absolute
	abs := filepath.Join(root, cleaned[1:])         // strip leading "/"
	if !strings.HasPrefix(abs+string(filepath.Separator), root+string(filepath.Separator)) && abs != root {
		return "", fmt.Errorf("path %q escapes %q", rel, root)
	}
	return abs, nil
}

// copyTree walks src and replicates it under dst, applying the optional
// excluded filter against paths relative to filterRoot.
func copyTree(src, dst, filterRoot string, excluded func(string) bool) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Compute path relative to filterRoot for ignore matching.
		if excluded != nil && filterRoot != "" {
			if rel, err := filepath.Rel(filterRoot, p); err == nil {
				if excluded(rel) {
					if info.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
			}
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyOne(p, target, info)
	})
}

// copyOne copies a single non-directory entry.
func copyOne(src, dst string, info os.FileInfo) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		_ = os.Remove(dst)
		return os.Symlink(target, dst)
	}
	if !info.Mode().IsRegular() {
		// Devices, fifos, sockets: silently skip.
		return nil
	}

	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()
	df, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(df, sf); err != nil {
		df.Close()
		return err
	}
	return df.Close()
}
