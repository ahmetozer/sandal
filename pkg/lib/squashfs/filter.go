package squashfs

import "strings"

// NewIncludeExcludeFilter builds a PathFilter function from include and exclude path lists.
//
// Rules:
//   - If no includes are specified, everything under "/" is included by default.
//   - A path is included if it falls under (or is a parent of) any include path.
//   - A path is excluded if it falls under any exclude path.
//   - Excludes take priority over includes.
//
// All paths should use "/" prefix (e.g. "/folder1/tmp", "/etc").
func NewIncludeExcludeFilter(includes, excludes []string) func(relPath string, isDir bool) bool {
	// Normalize: ensure trailing slash on directory prefixes for prefix matching
	norm := func(p string) string {
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		return strings.TrimSuffix(p, "/")
	}

	for i := range includes {
		includes[i] = norm(includes[i])
	}
	for i := range excludes {
		excludes[i] = norm(excludes[i])
	}

	return func(relPath string, isDir bool) bool {
		p := norm(relPath)

		// Check excludes first (higher priority)
		for _, exc := range excludes {
			// Excluded if path is the exclude dir or under it
			if p == exc || strings.HasPrefix(p, exc+"/") {
				return false
			}
		}

		// Check includes
		for _, inc := range includes {
			// Included if:
			// 1. Path is under an include prefix: /folder1/file matches include /folder1
			// 2. Path is a parent of an include prefix: /folder1 matches include /folder1/sub
			//    (directories that are ancestors of includes must be traversed)
			if p == inc || strings.HasPrefix(p, inc+"/") {
				return true
			}
			if isDir && strings.HasPrefix(inc, p+"/") {
				return true
			}
		}

		return false
	}
}
