package sandal

import (
	"fmt"
	"strings"
)

// ExtractFlag removes a flag and its value from args, returning the value and cleaned args.
// Handles both "-flag value" and "-flag=value" forms.
func ExtractFlag(args []string, name string, defaultVal string) (string, []string) {
	val := defaultVal
	prefix := "-" + name
	var clean []string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == prefix+"=" || strings.HasPrefix(arg, prefix+"=") {
			val = arg[len(prefix)+1:]
			continue
		}

		if arg == prefix && i+1 < len(args) {
			val = args[i+1]
			i++
			continue
		}

		clean = append(clean, arg)
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

// SplitFlagsArgs returns flags and child process args separated by "--".
func SplitFlagsArgs(args []string) (flagArgs []string, commandArgs []string, err error) {
	for childArgStartLoc, arg := range args {
		if arg == "--" {
			hostArgs := args[:childArgStartLoc]
			podArgs := args[childArgStartLoc+1:]
			if len(podArgs) < 1 {
				return hostArgs, podArgs, fmt.Errorf("there is no command provided")
			}
			return hostArgs, podArgs, nil
		}
	}
	return args, nil, fmt.Errorf("there is no command provided")
}
