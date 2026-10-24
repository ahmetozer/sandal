package config

import "strings"

// WithPanicReboot returns cmdline with panic=-1 prepended, so a guest kernel
// panic reboots at once instead of spinning in the panic loop. The VMM has no
// in-place reboot and treats the reset as the guest having stopped. A panic=
// already given by the user is kept. Only kernel parameters count: anything
// after "--" belongs to init. It is prepended so it stays before "--" and
// survives a loader that truncates long command lines.
func WithPanicReboot(cmdline string) string {
	if hasKernelParam(cmdline, "panic") {
		return cmdline
	}
	if cmdline == "" {
		return "panic=-1"
	}
	return "panic=-1 " + cmdline
}

// hasKernelParam reports whether the kernel part of cmdline sets key=.
// Splitting follows the kernel: whitespace outside double quotes separates
// parameters and a bare "--" ends them.
func hasKernelParam(cmdline, key string) bool {
	inQuote := false
	start := -1
	for i := 0; i <= len(cmdline); i++ {
		end := i == len(cmdline)
		if !end && cmdline[i] == '"' {
			inQuote = !inQuote
		}
		if end || (!inQuote && (cmdline[i] == ' ' || cmdline[i] == '\t' || cmdline[i] == '\n')) {
			if start >= 0 {
				param := strings.TrimPrefix(cmdline[start:i], `"`)
				if param == "--" {
					return false
				}
				if strings.HasPrefix(param, key+"=") {
					return true
				}
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	return false
}
