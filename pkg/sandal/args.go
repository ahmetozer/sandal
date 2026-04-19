package sandal

import (
	"fmt"
	"strings"
)

// ExtractFlag removes a flag and its value from args, returning the value and cleaned args.
// Handles "-flag value", "-flag=value", "--flag value", and "--flag=value" forms.
func ExtractFlag(args []string, name string, defaultVal string) (string, []string) {
	val := defaultVal
	prefixes := []string{"-" + name, "--" + name}
	var clean []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		matched := false

		for _, prefix := range prefixes {
			if arg == prefix+"=" || strings.HasPrefix(arg, prefix+"=") {
				val = arg[len(prefix)+1:]
				matched = true
				break
			}
			if arg == prefix && i+1 < len(args) {
				val = args[i+1]
				i++
				matched = true
				break
			}
		}

		if !matched {
			clean = append(clean, arg)
		}
	}

	return val, clean
}

// HasFlag checks if a flag is present in args (handles -flag, -flag=val forms).
func HasFlag(args []string, name string) bool {
	prefix := "-" + name
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == prefix || strings.HasPrefix(arg, prefix+"=") {
			return true
		}
	}
	return false
}

// RemoveBoolFlag removes a boolean flag (no value) from args.
// Handles both "-flag" and "--flag" forms.
func RemoveBoolFlag(args []string, name string) []string {
	var clean []string
	for _, arg := range args {
		if arg == "-"+name || arg == "--"+name {
			continue
		}
		clean = append(clean, arg)
	}
	return clean
}

// SplitFlagsArgs returns flags and child process args separated by "--".
// If "--" is absent, all args are treated as flags and commandArgs is nil.
// The caller is responsible for resolving the command from image config
// defaults when commandArgs is empty.
func SplitFlagsArgs(args []string) (flagArgs []string, commandArgs []string, err error) {
	for childArgStartLoc, arg := range args {
		if arg == "--" {
			hostArgs := args[:childArgStartLoc]
			podArgs := args[childArgStartLoc+1:]
			if len(podArgs) < 1 {
				return hostArgs, podArgs, fmt.Errorf("there is no command provided after '--'")
			}
			return hostArgs, podArgs, nil
		}
	}
	// No "--" found — all args are flags, command may come from image config.
	return args, nil, nil
}
