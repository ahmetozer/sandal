//go:build linux && arm64

package kernel

import (
	_ "embed"
)

// preinitBinary is a tiny static ARM64 ELF that mounts /proc and /dev,
// sets up /dev/console as stdin/stdout/stderr, then execve's /sandal-init.
// This is required because Go's runtime needs /proc and valid fds to initialize.
//
// Generated from preinit_arm64.s via:
//   go generate ./pkg/vm/kernel/
//
//go:generate sh -c "aarch64-linux-gnu-as -o preinit_arm64.o preinit_arm64.S && aarch64-linux-gnu-ld -static -o preinit_arm64.bin preinit_arm64.o && rm preinit_arm64.o"
//go:embed preinit_arm64.bin
var preinitBinary []byte
