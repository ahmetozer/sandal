package progress

// Phase identifies which stage of a long-running operation is reporting progress.
type Phase int

const (
	PhaseDownload Phase = iota
	PhaseExtract
	PhaseSquashfs
)

func (p Phase) String() string {
	switch p {
	case PhaseDownload:
		return "download"
	case PhaseExtract:
		return "extract"
	case PhaseSquashfs:
		return "squashfs"
	default:
		return "unknown"
	}
}

// Event represents a progress status update from a long-running operation.
// Library code sends these on an optional channel; the consumer (CLI, web, etc.)
// decides how to render them.
type Event struct {
	Phase   Phase
	TaskID  string // e.g. "layer 1", "layer 2", "squashfs"
	Current int64  // bytes processed so far
	Total   int64  // total bytes expected (0 = indeterminate)
	Done    bool   // true when this task is complete
}
