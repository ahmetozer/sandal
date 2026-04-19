package build

import (
	"bufio"
	"io"
	"path"
	"strings"
)

// IgnoreMatcher decides whether a path (relative to the build context) is
// excluded by .dockerignore rules.
//
// Pattern rules (subset of Docker's):
//   - blank lines and `#` comments are ignored
//   - patterns are slash-paths; backslashes are converted to slashes
//   - leading `!` negates a previous match
//   - `**` matches across path segments; `*` matches within a segment;
//     `?` matches one char
//   - trailing `/` only affects display; we always match against the
//     path with no trailing slash
type IgnoreMatcher struct {
	rules []ignoreRule
}

type ignoreRule struct {
	pattern string
	negate  bool
}

// LoadIgnore parses a .dockerignore file from r. nil reader returns an
// empty matcher (everything included).
func LoadIgnore(r io.Reader) (*IgnoreMatcher, error) {
	m := &IgnoreMatcher{}
	if r == nil {
		return m, nil
	}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		negate := false
		if strings.HasPrefix(line, "!") {
			negate = true
			line = strings.TrimSpace(line[1:])
			if line == "" {
				continue
			}
		}
		line = strings.ReplaceAll(line, "\\", "/")
		line = strings.TrimSuffix(line, "/")
		// Strip leading "./" — paths are matched relative to context root.
		line = strings.TrimPrefix(line, "./")
		m.rules = append(m.rules, ignoreRule{pattern: line, negate: negate})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return m, nil
}

// Excluded returns true if the given path (relative, slash-separated) is
// excluded by the ignore rules. Later rules override earlier ones.
func (m *IgnoreMatcher) Excluded(rel string) bool {
	if m == nil || len(m.rules) == 0 {
		return false
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.TrimPrefix(rel, "./")
	rel = strings.TrimPrefix(rel, "/")

	excluded := false
	for _, r := range m.rules {
		if matchPattern(r.pattern, rel) {
			excluded = !r.negate
		}
	}
	return excluded
}

// matchPattern implements the .dockerignore subset described above.
func matchPattern(pattern, name string) bool {
	// Fast paths.
	if pattern == name {
		return true
	}
	// path.Match handles most globs; fall back for `**` cross-segment.
	if !strings.Contains(pattern, "**") {
		// path.Match doesn't cross segments; emulate prefix-of-dir match
		// for simple "dir" patterns by also matching "dir/<anything>".
		if ok, _ := path.Match(pattern, name); ok {
			return true
		}
		// "node_modules" should also exclude "node_modules/foo".
		if !strings.ContainsAny(pattern, "*?[") {
			if strings.HasPrefix(name, pattern+"/") {
				return true
			}
		}
		return false
	}
	return matchDoubleStar(pattern, name)
}

// matchDoubleStar is a small recursive globber that supports `**`.
// `**` matches zero or more path segments; `*` matches within a segment.
func matchDoubleStar(pattern, name string) bool {
	idx := strings.Index(pattern, "**")
	if idx < 0 {
		ok, _ := path.Match(pattern, name)
		return ok
	}

	head := strings.TrimSuffix(pattern[:idx], "/")
	tail := strings.TrimPrefix(pattern[idx+2:], "/")

	// Split name into (prefix, rest) at each "/" boundary (plus the
	// empty and full-length boundaries) and see if head matches the
	// prefix AND the remaining tail matches some tail of rest.
	matchTail := func(rest string) bool {
		if tail == "" {
			return true // ** alone matches everything remaining
		}
		// tail must match either "rest" wholesale, or any sub-path of it.
		if matchDoubleStar(tail, rest) {
			return true
		}
		for i := 0; i < len(rest); i++ {
			if rest[i] == '/' && matchDoubleStar(tail, rest[i+1:]) {
				return true
			}
		}
		return false
	}

	if head == "" {
		return matchTail(name)
	}

	for i := 0; i <= len(name); i++ {
		if i > 0 && i < len(name) && name[i] != '/' {
			continue
		}
		prefix := name[:i]
		rest := ""
		if i < len(name) {
			rest = name[i+1:]
		}
		if ok, _ := path.Match(head, prefix); ok {
			if matchTail(rest) {
				return true
			}
		}
	}
	return false
}
