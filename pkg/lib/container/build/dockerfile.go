package build

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
)

// ParseDockerfile reads a Dockerfile from r and returns its instructions
// in source order. It handles:
//   - line continuation with '\' at end of line
//   - '#' comments (full-line; not mid-line)
//   - parser directives (# syntax=, # escape=) — escape directive honoured
//   - JSON exec-form ([ "a", "b" ]) vs shell-form
//
// Instructions are NOT yet split into stages; call SplitStages on the result.
func ParseDockerfile(r io.Reader) ([]Instruction, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024) // up to 4MB lines

	escape := byte('\\')
	directivesAllowed := true

	var instrs []Instruction
	var buf strings.Builder
	startLine := 0
	curLine := 0

	flush := func() error {
		raw := strings.TrimSpace(buf.String())
		buf.Reset()
		if raw == "" {
			return nil
		}
		instr, err := parseInstruction(raw, startLine)
		if err != nil {
			return err
		}
		instrs = append(instrs, instr)
		return nil
	}

	for scanner.Scan() {
		curLine++
		line := scanner.Text()

		// Strip CR (Windows line endings).
		line = strings.TrimRight(line, "\r")

		trimmed := strings.TrimLeft(line, " \t")

		// Parser directives only apply at the very top of the file before
		// any non-directive content.
		if directivesAllowed && strings.HasPrefix(trimmed, "#") {
			if d, v, ok := parseDirective(trimmed); ok {
				if d == "escape" && len(v) == 1 && (v == "\\" || v == "`") {
					escape = v[0]
				}
				continue
			}
			directivesAllowed = false
			continue // comment
		}

		if strings.HasPrefix(trimmed, "#") {
			continue // comment
		}

		// Blank line — flush any pending instruction.
		if strings.TrimSpace(line) == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			directivesAllowed = false
			continue
		}

		directivesAllowed = false

		if buf.Len() == 0 {
			startLine = curLine
		}

		// Check for line continuation.
		stripped := strings.TrimRight(line, " \t")
		if len(stripped) > 0 && stripped[len(stripped)-1] == escape {
			// Append without the trailing escape and continue.
			buf.WriteString(stripped[:len(stripped)-1])
			buf.WriteByte(' ')
			continue
		}

		buf.WriteString(line)
		if err := flush(); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	// Flush any trailing content (file without final newline).
	if err := flush(); err != nil {
		return nil, err
	}
	return instrs, nil
}

// parseDirective matches lines of the form `# key=value` at the head of
// the file. Returns (key, value, true) on match.
func parseDirective(line string) (string, string, bool) {
	if !strings.HasPrefix(line, "#") {
		return "", "", false
	}
	rest := strings.TrimSpace(line[1:])
	eq := strings.IndexByte(rest, '=')
	if eq < 1 {
		return "", "", false
	}
	key := strings.TrimSpace(rest[:eq])
	val := strings.TrimSpace(rest[eq+1:])
	if key == "" || val == "" {
		return "", "", false
	}
	// Directive keys are simple identifiers.
	for _, r := range key {
		if !unicode.IsLetter(r) && r != '_' && r != '-' {
			return "", "", false
		}
	}
	return strings.ToLower(key), val, true
}

// parseInstruction parses a single (already line-continuation-joined) Dockerfile
// instruction string into an Instruction.
func parseInstruction(raw string, line int) (Instruction, error) {
	// Split off the keyword (first whitespace-delimited token).
	keyword, rest := splitFirst(raw)
	kind := parseKind(keyword)
	if kind == InstrUnknown {
		return Instruction{}, fmt.Errorf("dockerfile line %d: unknown instruction %q", line, keyword)
	}

	in := Instruction{
		Kind: kind,
		Raw:  raw,
		Line: line,
	}

	// Pull leading --flag=value tokens (supported by COPY, ADD, FROM, RUN).
	rest, in.Flags = extractFlags(rest)

	// JSON exec-form: leading '[' indicates an exec-form array.
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(rest), &arr); err == nil {
			in.JSON = true
			in.Args = arr
			return in, nil
		}
		// fall through to shell-form parsing on JSON parse failure
	}

	// Shell-form: per-instruction parsing rules.
	switch kind {
	case InstrEnv, InstrLabel, InstrArg:
		// KEY=VALUE pairs (whitespace-separated, with quote handling).
		// Also accepts the legacy ENV KEY VALUE form (single pair).
		pairs, err := parseKVList(rest)
		if err != nil {
			return Instruction{}, fmt.Errorf("dockerfile line %d: %s: %w", line, kind, err)
		}
		in.Args = pairs

	default:
		// Generic shell-form: keep the rest verbatim as a single arg
		// for RUN/CMD/ENTRYPOINT, or split on whitespace for others.
		switch kind {
		case InstrRun, InstrCmd, InstrEntrypoint:
			in.Args = []string{rest}
		default:
			in.Args = shellSplit(rest)
		}
	}

	return in, nil
}

// splitFirst returns the first whitespace-delimited token and the remainder.
func splitFirst(s string) (string, string) {
	s = strings.TrimLeft(s, " \t")
	end := -1
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			end = i
			break
		}
	}
	if end < 0 {
		return s, ""
	}
	return s[:end], strings.TrimLeft(s[end:], " \t")
}

// extractFlags pulls leading --key=value tokens off rest.
func extractFlags(rest string) (string, map[string]string) {
	flags := map[string]string{}
	for {
		rest = strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(rest, "--") {
			break
		}
		// Find end of token (whitespace).
		end := -1
		for i := 0; i < len(rest); i++ {
			if rest[i] == ' ' || rest[i] == '\t' {
				end = i
				break
			}
		}
		var tok string
		if end < 0 {
			tok = rest
			rest = ""
		} else {
			tok = rest[:end]
			rest = rest[end:]
		}
		// "--" alone is a separator, not a flag — stop.
		if tok == "--" {
			break
		}
		eq := strings.IndexByte(tok, '=')
		if eq < 0 {
			// Bare boolean flag (rare in Dockerfile); record as "true".
			flags[tok[2:]] = "true"
		} else {
			flags[tok[2:eq]] = tok[eq+1:]
		}
	}
	if len(flags) == 0 {
		return rest, nil
	}
	return rest, flags
}

// shellSplit splits a string on unquoted whitespace, honouring " and ' quotes.
func shellSplit(s string) []string {
	var out []string
	var cur strings.Builder
	inSingle, inDouble := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			} else {
				cur.WriteByte(c)
			}
		case inDouble:
			if c == '"' {
				inDouble = false
			} else if c == '\\' && i+1 < len(s) {
				cur.WriteByte(s[i+1])
				i++
			} else {
				cur.WriteByte(c)
			}
		case c == '\'':
			inSingle = true
		case c == '"':
			inDouble = true
		case c == ' ' || c == '\t':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// parseKVList parses ENV/LABEL/ARG argument lists into "KEY=VALUE" strings.
// Supports two forms:
//   ENV KEY VALUE                  (legacy single-pair, value is rest of line)
//   ENV KEY1=val1 KEY2="hello world"   (multi-pair)
func parseKVList(rest string) ([]string, error) {
	// Detect legacy form: first token has no '=' and there is no later '='.
	first, after := splitFirst(rest)
	if first == "" {
		return nil, fmt.Errorf("missing key")
	}
	if !strings.ContainsRune(first, '=') && !strings.ContainsRune(after, '=') {
		val := strings.TrimSpace(after)
		// Strip surrounding quotes if present.
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		return []string{first + "=" + val}, nil
	}
	// Multi-pair form: tokenize like a shell would.
	tokens := shellSplit(rest)
	for _, t := range tokens {
		if !strings.ContainsRune(t, '=') {
			return nil, fmt.Errorf("expected KEY=VALUE, got %q", t)
		}
	}
	return tokens, nil
}
