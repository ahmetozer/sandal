package run

import (
	"fmt"
	"strings"
)

// hasFlag checks if a flag is present in args (handles -flag, -flag=val forms)
func hasFlag(args []string, name string) bool {
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

// extractFlag removes a flag and its value from args, returning the value and cleaned args.
// Handles both "-flag value" and "-flag=value" forms.
func extractFlag(args []string, name string, defaultVal string) (string, []string) {
	val := defaultVal
	prefix := "-" + name
	var clean []string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		// -flag=value form
		if strings.HasPrefix(arg, prefix+"=") {
			val = arg[len(prefix)+1:]
			continue
		}

		// -flag value form
		if arg == prefix && i+1 < len(args) {
			val = args[i+1]
			i++ // skip value
			continue
		}

		clean = append(clean, arg)
	}

	return val, clean
}

// scanMountPaths scans args for -v flag values and returns the host paths
// that need VirtioFS shares. Does not modify args.
func scanMountPaths(args []string) []string {
	var paths []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		if args[i] == "-v" && i+1 < len(args) {
			i++
			hostPath := args[i]
			if parts := strings.SplitN(hostPath, ":", 2); len(parts) >= 1 {
				hostPath = parts[0]
			}
			paths = append(paths, hostPath)
		}
	}
	return paths
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
