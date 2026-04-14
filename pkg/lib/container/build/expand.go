package build

import "strings"

// Expand performs Dockerfile-style ${VAR} and $VAR substitution against
// the given variable map. Unknown variables expand to empty string.
//
// Supports:
//   $NAME                         basic
//   ${NAME}                       braced
//   ${NAME:-default}              default if unset/empty
//   ${NAME:+alternate}            alternate if set/non-empty
//
// Backslash escapes the next byte ($ \$ becomes literal $).
func Expand(s string, vars map[string]string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			b.WriteByte(s[i+1])
			i++
			continue
		}
		if c != '$' {
			b.WriteByte(c)
			continue
		}
		// $ at end of string — literal.
		if i+1 >= len(s) {
			b.WriteByte('$')
			continue
		}
		next := s[i+1]
		if next == '{' {
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				// No closing brace — treat as literal.
				b.WriteByte('$')
				continue
			}
			expr := s[i+2 : i+2+end]
			b.WriteString(evalBracedExpr(expr, vars))
			i += 2 + end
			continue
		}
		// Unbraced: read identifier characters.
		j := i + 1
		for j < len(s) && isIdentChar(s[j]) {
			j++
		}
		if j == i+1 {
			// Lone $ — literal.
			b.WriteByte('$')
			continue
		}
		name := s[i+1 : j]
		b.WriteString(vars[name])
		i = j - 1
	}
	return b.String()
}

func evalBracedExpr(expr string, vars map[string]string) string {
	// Find ":-" or ":+" operator.
	if idx := strings.Index(expr, ":-"); idx > 0 {
		name := expr[:idx]
		def := expr[idx+2:]
		if v, ok := vars[name]; ok && v != "" {
			return v
		}
		return def
	}
	if idx := strings.Index(expr, ":+"); idx > 0 {
		name := expr[:idx]
		alt := expr[idx+2:]
		if v, ok := vars[name]; ok && v != "" {
			return alt
		}
		return ""
	}
	return vars[expr]
}

func isIdentChar(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}
