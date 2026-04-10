package progress

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// StartRenderer consumes events from ch in a background goroutine and
// renders progress to out (typically os.Stderr). It returns a channel
// that is closed when the renderer finishes (after ch is closed).
func StartRenderer(ch <-chan Event, out io.Writer) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		r := renderer{
			out:      out,
			isTTY:    isTTY(out),
			termWidth: getTermWidth(out),
			tasks:    make(map[string]*Event),
		}
		for ev := range ch {
			r.update(ev)
		}
		r.clearLine()
	}()
	return done
}

func isTTY(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		return isTerminal(f)
	}
	return false
}

func getTermWidth(w io.Writer) int {
	if f, ok := w.(*os.File); ok {
		return terminalWidth(f)
	}
	return 80
}

type renderer struct {
	out              io.Writer
	isTTY            bool
	termWidth        int
	tasks            map[string]*Event
	order            []string
	lastLen          int
	downloadFinished bool
}

func (r *renderer) update(ev Event) {
	if _, exists := r.tasks[ev.TaskID]; !exists {
		r.order = append(r.order, ev.TaskID)
	}
	r.tasks[ev.TaskID] = &ev

	// When all download tasks are done, print summary and clear them.
	if ev.Phase == PhaseDownload && ev.Done && !r.downloadFinished {
		if r.allDone(PhaseDownload) {
			r.clearLine()
			r.printDownloadSummary()
			r.downloadFinished = true
			return
		}
	}

	// When extract is done, print summary.
	if ev.Phase == PhaseExtract && ev.Done {
		r.clearLine()
		fmt.Fprintf(r.out, "  Extracted %d layers\n", ev.Current)
		return
	}

	// When squashfs is done, print summary.
	if ev.Phase == PhaseSquashfs && ev.Done {
		r.clearLine()
		fmt.Fprintf(r.out, "  Created squashfs (%s)\n", humanBytes(ev.Current))
		return
	}

	if r.isTTY {
		r.renderLine(ev.Phase)
	}
}

func (r *renderer) allDone(phase Phase) bool {
	for _, id := range r.order {
		ev := r.tasks[id]
		if ev.Phase == phase && !ev.Done {
			return false
		}
	}
	return true
}

func (r *renderer) printDownloadSummary() {
	var total int64
	var count int
	for _, id := range r.order {
		ev := r.tasks[id]
		if ev.Phase == PhaseDownload {
			total += ev.Current
			count++
		}
	}
	fmt.Fprintf(r.out, "  Downloaded %d layers (%s)\n", count, humanBytes(total))
}

func (r *renderer) renderLine(phase Phase) {
	// Collect tasks for the active phase.
	sorted := make([]string, 0, len(r.order))
	for _, id := range r.order {
		if r.tasks[id].Phase == phase {
			sorted = append(sorted, id)
		}
	}
	sort.Strings(sorted)

	var overallCurrent, overallTotal int64
	for _, id := range sorted {
		ev := r.tasks[id]
		overallCurrent += ev.Current
		overallTotal += ev.Total
	}

	var label string
	switch phase {
	case PhaseDownload:
		label = "Downloading"
	case PhaseExtract:
		label = "Extracting layers"
	case PhaseSquashfs:
		label = "Creating squashfs"
	default:
		label = phase.String()
	}

	// Build the main progress part.
	var line string
	if phase == PhaseExtract {
		// Extract uses layer counts, not bytes.
		line = fmt.Sprintf("  %s: %d/%d", label, overallCurrent, overallTotal)
	} else if overallTotal > 0 {
		pct := int(overallCurrent * 100 / overallTotal)
		line = fmt.Sprintf("  %s: %d%% (%s/%s)",
			label, pct, humanBytes(overallCurrent), humanBytes(overallTotal))
	} else {
		line = fmt.Sprintf("  %s: %s", label, humanBytes(overallCurrent))
	}

	// For download phase, show only active (non-done) layers.
	if phase == PhaseDownload {
		var parts []string
		for _, id := range sorted {
			ev := r.tasks[id]
			if ev.Done {
				continue
			}
			if ev.Total > 0 {
				pct := int(ev.Current * 100 / ev.Total)
				parts = append(parts, fmt.Sprintf("%s: %d%%", id, pct))
			} else {
				parts = append(parts, fmt.Sprintf("%s: %s", id, humanBytes(ev.Current)))
			}
		}
		if len(parts) > 0 {
			line += " [" + strings.Join(parts, ", ") + "]"
		}
	}

	// Truncate to terminal width to prevent wrapping.
	maxLen := r.termWidth - 1
	if maxLen > 0 && len(line) > maxLen {
		line = line[:maxLen]
	}

	// Pad with spaces to overwrite previous longer line.
	if len(line) < r.lastLen {
		line += strings.Repeat(" ", r.lastLen-len(line))
	}
	r.lastLen = len(line)

	fmt.Fprintf(r.out, "\r%s", line)
}

func (r *renderer) clearLine() {
	if r.isTTY && r.lastLen > 0 {
		fmt.Fprintf(r.out, "\r%s\r", strings.Repeat(" ", r.lastLen))
		r.lastLen = 0
	}
}

func humanBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
