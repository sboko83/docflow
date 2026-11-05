package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"bsv-docflow/internal/release"
)

const (
	reportBarWidth = 28
	// reportBarInterval throttles redraws of the download line.
	reportBarInterval = 80 * time.Millisecond
)

// updateReport narrates `docflow update` on stderr: one "update: ..." line per
// step and, on a terminal, a live download bar. Without a terminal (a pipe, a
// CI log) the bar is replaced by one summary line, so the log stays readable.
type updateReport struct {
	w       io.Writer
	tty     bool
	started time.Time
	drawn   time.Time
	barOpen bool
	total   int64
}

func newUpdateReport(w io.Writer) *updateReport {
	return &updateReport{w: w, tty: stderrIsTerminal(w)}
}

// say prints one step line.
func (r *updateReport) say(format string, args ...any) {
	r.endBar()
	fmt.Fprintf(r.w, "update: "+format+"\n", args...)
}

// endBar terminates an unfinished download line, e.g. before an error text.
func (r *updateReport) endBar() {
	if r.barOpen {
		fmt.Fprintln(r.w)
		r.barOpen = false
	}
}

// fetch is the release.FetchProgress callback.
func (r *updateReport) fetch(e release.Event) {
	switch e.Stage {
	case release.StageDownload:
		if e.Done == 0 {
			r.started, r.total = time.Now(), e.Total
			if e.Total > 0 {
				r.say("downloading %s (%s)", e.Name, formatBytes(e.Total))
			} else {
				r.say("downloading %s", e.Name)
			}
			return
		}
		r.download(e)
	case release.StageVerify:
		if !r.tty && r.total <= 0 {
			// Without a size no chunk counts as the last one; the summary
			// line comes once the whole archive is read.
			r.say("downloaded %s in %s", formatBytes(e.Done), time.Since(r.started).Round(100*time.Millisecond))
		}
		r.say("checksum ok (sha256)")
	case release.StageExtract:
		r.say("extracting")
	}
}

func (r *updateReport) download(e release.Event) {
	finished := e.Total > 0 && e.Done >= e.Total
	if !r.tty {
		// One summary line instead of a bar nobody can redraw.
		if finished {
			r.say("downloaded %s in %s", formatBytes(e.Done), time.Since(r.started).Round(100*time.Millisecond))
		}
		return
	}
	now := time.Now()
	if !finished && now.Sub(r.drawn) < reportBarInterval {
		return
	}
	r.drawn = now
	fmt.Fprint(r.w, "\r"+r.barLine(e, now.Sub(r.started)))
	r.barOpen = true
	if finished {
		r.endBar()
	}
}

// barLine renders "  [=====>    ]  52%  3.6 / 6.9 MB  2.1 MB/s", padded to a
// fixed width so a shorter redraw leaves no leftovers.
func (r *updateReport) barLine(e release.Event, elapsed time.Duration) string {
	speed := ""
	if elapsed > 200*time.Millisecond {
		speed = "  " + formatBytes(int64(float64(e.Done)/elapsed.Seconds())) + "/s"
	}
	var line string
	if e.Total > 0 {
		filled := int(int64(reportBarWidth) * e.Done / e.Total)
		if filled > reportBarWidth {
			filled = reportBarWidth
		}
		bar := strings.Repeat("=", filled)
		if filled < reportBarWidth {
			bar += ">" + strings.Repeat(" ", reportBarWidth-filled-1)
		}
		line = fmt.Sprintf("  [%s] %3d%%  %s / %s%s", bar, 100*e.Done/e.Total, formatBytes(e.Done), formatBytes(e.Total), speed)
	} else {
		line = fmt.Sprintf("  %s%s", formatBytes(e.Done), speed)
	}
	return fmt.Sprintf("%-*s", 2+reportBarWidth+40, line)
}

// formatBytes prints a size as KB or MB with one decimal.
func formatBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// displayRelease shortens a release base URL for messages: no scheme.
func displayRelease(base string) string {
	if i := strings.Index(base, "://"); i >= 0 {
		return base[i+3:]
	}
	return base
}
