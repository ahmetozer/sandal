// Package build implements Dockerfile parsing and image building.
package build

import "fmt"

// InstrKind identifies a Dockerfile instruction.
type InstrKind int

const (
	InstrUnknown InstrKind = iota
	InstrFrom
	InstrRun
	InstrCopy
	InstrAdd
	InstrEnv
	InstrWorkDir
	InstrUser
	InstrCmd
	InstrEntrypoint
	InstrLabel
	InstrExpose
	InstrArg
	InstrVolume
	InstrShell
	InstrStopSignal
)

// String returns the uppercase Dockerfile keyword.
func (k InstrKind) String() string {
	switch k {
	case InstrFrom:
		return "FROM"
	case InstrRun:
		return "RUN"
	case InstrCopy:
		return "COPY"
	case InstrAdd:
		return "ADD"
	case InstrEnv:
		return "ENV"
	case InstrWorkDir:
		return "WORKDIR"
	case InstrUser:
		return "USER"
	case InstrCmd:
		return "CMD"
	case InstrEntrypoint:
		return "ENTRYPOINT"
	case InstrLabel:
		return "LABEL"
	case InstrExpose:
		return "EXPOSE"
	case InstrArg:
		return "ARG"
	case InstrVolume:
		return "VOLUME"
	case InstrShell:
		return "SHELL"
	case InstrStopSignal:
		return "STOPSIGNAL"
	}
	return "UNKNOWN"
}

// parseKind maps a Dockerfile keyword to an InstrKind (case-insensitive).
func parseKind(word string) InstrKind {
	switch upperASCII(word) {
	case "FROM":
		return InstrFrom
	case "RUN":
		return InstrRun
	case "COPY":
		return InstrCopy
	case "ADD":
		return InstrAdd
	case "ENV":
		return InstrEnv
	case "WORKDIR":
		return InstrWorkDir
	case "USER":
		return InstrUser
	case "CMD":
		return InstrCmd
	case "ENTRYPOINT":
		return InstrEntrypoint
	case "LABEL":
		return InstrLabel
	case "EXPOSE":
		return InstrExpose
	case "ARG":
		return InstrArg
	case "VOLUME":
		return InstrVolume
	case "SHELL":
		return InstrShell
	case "STOPSIGNAL":
		return InstrStopSignal
	}
	return InstrUnknown
}

// Instruction is one parsed Dockerfile instruction.
type Instruction struct {
	Kind  InstrKind
	Args  []string          // shell-form tokens, OR JSON exec-form elements
	JSON  bool              // true if original was ["a","b"] array form
	Flags map[string]string // --from, --chown, --chmod, --platform
	Raw   string            // original source (for History.CreatedBy)
	Line  int               // 1-based source line of the instruction start
}

func (i Instruction) String() string {
	return fmt.Sprintf("%s@%d %s", i.Kind, i.Line, i.Raw)
}

// upperASCII is a small, allocation-free uppercaser for ASCII keywords.
// Dockerfile keywords are all 7-bit ASCII so we avoid unicode tables.
func upperASCII(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
