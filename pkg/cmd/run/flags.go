package run

import (
	"fmt"
	"os"
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

// SocketMount represents a Unix socket to relay between host and guest via vsock.
type SocketMount struct {
	HostPath  string
	GuestPath string
}

// scanMountPaths scans args for -v flag values and returns the host paths
// that need VirtioFS shares and socket mounts that need vsock relay.
// Does not modify args.
//
// Detection rules for each -v host:guest[:opts] entry:
//   - If opts contains "sock" -> socket share (guest->host, path may not exist yet)
//   - Else if stat(host) returns a socket -> socket share (host->guest, auto-detect)
//   - Else -> VirtioFS share (existing behavior)
func scanMountPaths(args []string) ([]string, []SocketMount) {
	var paths []string
	var sockets []SocketMount
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		if args[i] == "-v" && i+1 < len(args) {
			i++
			val := args[i]

			// Parse host:guest:opts
			parts := strings.SplitN(val, ":", 3)
			hostPath := parts[0]
			guestPath := hostPath
			opts := ""
			if len(parts) >= 2 {
				guestPath = parts[1]
			}
			if len(parts) >= 3 {
				opts = parts[2]
			}

			// Check if this is a socket mount
			if strings.Contains(opts, "sock") {
				sockets = append(sockets, SocketMount{HostPath: hostPath, GuestPath: guestPath})
				continue
			}

			// Auto-detect existing sockets
			if fi, err := os.Stat(hostPath); err == nil && fi.Mode().Type()&os.ModeSocket != 0 {
				sockets = append(sockets, SocketMount{HostPath: hostPath, GuestPath: guestPath})
				continue
			}

			paths = append(paths, hostPath)
		}
	}
	return paths, sockets
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
