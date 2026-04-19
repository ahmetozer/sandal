package squash

import (
	"archive/tar"
	"path"
	"sort"
	"strings"
)

// pathIndex tracks the post-whiteout path set across OCI image layers.
// It is updated in tar-stream order: .wh. file markers and .wh..wh..opq
// directory markers remove earlier entries, subsequent layers re-add
// them if present. Used as ground truth when building the squashfs so
// the writer does not depend on os.ReadDir against an overlayfs-backed
// tmpDir, which can silently return truncated listings.
//
// Values are the tar Typeflag, so counts by type (e.g. regular files)
// can be computed without re-stating the disk.
type pathIndex struct {
	paths map[string]byte
}

func newPathIndex() *pathIndex {
	return &pathIndex{paths: map[string]byte{}}
}

// record applies one tar header to the index. name is the tar header
// name; typeflag is the header's Typeflag field.
func (p *pathIndex) record(name string, typeflag byte) {
	name = cleanPath(name)
	if name == "" {
		return
	}
	dir := path.Dir(name)
	base := path.Base(name)

	if base == ".wh..wh..opq" {
		// Opaque directory: remove descendants of `dir` from lower layers
		// but keep `dir` itself (it continues to exist in the merged view).
		target := dir
		if target == "." {
			target = ""
		}
		p.removeDescendants(target)
		return
	}
	if strings.HasPrefix(base, ".wh.") {
		target := path.Join(dir, base[len(".wh."):])
		p.removeTree(target)
		return
	}

	switch typeflag {
	case tar.TypeReg, tar.TypeSymlink, tar.TypeLink, tar.TypeDir:
		p.paths[name] = typeflag
	}
}

// removeTree drops the named path and every descendant of it.
func (p *pathIndex) removeTree(target string) {
	delete(p.paths, target)
	p.removeDescendants(target)
}

// removeDescendants drops every entry whose path is strictly below target.
// target itself is retained. An empty target means strip everything
// (opaque root).
func (p *pathIndex) removeDescendants(target string) {
	if target == "" {
		for k := range p.paths {
			delete(p.paths, k)
		}
		return
	}
	prefix := target + "/"
	for k := range p.paths {
		if strings.HasPrefix(k, prefix) {
			delete(p.paths, k)
		}
	}
}

// sortedPaths returns the final path set in ascending order.
func (p *pathIndex) sortedPaths() []string {
	out := make([]string, 0, len(p.paths))
	for k := range p.paths {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// len returns the number of paths currently tracked.
func (p *pathIndex) len() int {
	return len(p.paths)
}

// countRegularFiles returns the number of regular-file entries in the
// index. rootPath is unused for now but kept in the signature to match
// the walker-based counter so callers can swap implementations.
// Hard links are counted alongside regular files because extractLayerRaw
// materialises them as real files on disk.
func (p *pathIndex) countRegularFiles(rootPath string) int {
	_ = rootPath
	n := 0
	for _, t := range p.paths {
		if t == tar.TypeReg || t == tar.TypeLink {
			n++
		}
	}
	return n
}
