// Package vmbin provides the Linux sandal binary used as /init inside
// VM guests. On linux the running executable is used directly; on darwin
// the Linux binary is embedded at build time via go:generate + go:embed.
//
// The repo commits a text placeholder at pkg/sandal/vmbin/linux-sandal so
// the tree always compiles. Before building sandal on darwin, run:
//
//	go generate ./pkg/sandal/vmbin
//
// which cross-compiles the current tree for GOOS=linux into that file,
// replacing the placeholder. Don't commit the resulting ELF back; use
// `git restore pkg/sandal/vmbin/linux-sandal` to recover the sentinel.
package vmbin
