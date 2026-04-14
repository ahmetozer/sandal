//go:build linux

package vmbin

import (
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
)

// Linux returns the bytes of the sandal binary to be used as /init in
// a VM guest. On linux this is the running executable.
//
// Returns an error if the binary is dynamically linked, since the
// minimal initramfs has no dynamic loader (e.g. /lib/ld-linux-aarch64.so.1)
// and the kernel would panic with a misleading "init failed (error -2)"
// at boot. This check fails fast on the host with a clear message.
func Linux() ([]byte, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolving self binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	if err := assertStaticallyLinked(exe); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(exe)
	if err != nil {
		return nil, fmt.Errorf("reading self binary %s: %w", exe, err)
	}
	return data, nil
}

// assertStaticallyLinked returns an error if the ELF binary at path has a
// PT_INTERP program header (i.e. requires a dynamic loader). The minimal
// VM initramfs has no dynamic loader, so a dynamically linked /init would
// kernel-panic at boot. The Makefile builds with CGO_ENABLED=0 to avoid
// this; ad-hoc `go build` invocations may produce a CGO binary, hence the
// runtime guard.
func assertStaticallyLinked(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		// Not an ELF (or unreadable) — skip the check.
		return nil
	}
	defer f.Close()

	for _, prog := range f.Progs {
		if prog.Type == elf.PT_INTERP {
			return fmt.Errorf("sandal binary %s is dynamically linked and cannot be used as VM init "+
				"(would kernel-panic at boot — initramfs has no dynamic loader). "+
				"Rebuild with CGO_ENABLED=0: `CGO_ENABLED=0 go build -o sandal .` or `make build-linux`", path)
		}
	}
	return nil
}
