//go:build linux || darwin

// Package clean implements the scan-and-diff backend for `sandal clear`.
// It identifies disk artifacts that can be safely reclaimed and exposes
// a dry-run preview so operators can see what would be deleted before
// anything is touched.
package clean

// Kind classifies what kind of artifact an Action targets. Used in the
// dry-run report and in log output.
type Kind string

const (
	KindContainer       Kind = "container"
	KindImage           Kind = "image"
	KindSnapshot        Kind = "snapshot"
	KindOrphanChangeDir Kind = "orphan-changedir"
	KindOrphanChangeImg Kind = "orphan-img"
	KindKernelCache     Kind = "kernel-cache"
	KindTemp            Kind = "temp"
)

// Action describes one reclaimable artifact. Plan* functions return a
// slice of these; Apply consumes them.
type Action struct {
	// Path is the absolute filesystem path that will be removed.
	Path string
	// Kind classifies the artifact.
	Kind Kind
	// Reason is a short human-readable explanation shown in the dry-run
	// report and in log output (e.g. "state file missing").
	Reason string
	// Bytes is the best-effort on-disk size reported by os.Lstat / walk.
	// Used only for the summary; 0 is acceptable when size can't be
	// determined cheaply.
	Bytes int64
}
