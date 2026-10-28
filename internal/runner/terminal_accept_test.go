//go:build windows || linux || darwin

package runner

import (
	"regexp"
	"strings"
	"testing"
)

// Shared frame rules of the terminal acceptance: the same checks run on a real
// PTY (Linux, macOS) and on a real pseudo console (Windows), so a frame defect
// on one platform is not settled by the other platform's green run.
var (
	unitHeader   = regexp.MustCompile(`(?m)^┌─ [0-9]{2}:[0-9]{2} · P001\.1\.1: `)
	spinnerFrame = regexp.MustCompile(`│  [⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏] ([^\r\n]*)`)
	ansiControl  = regexp.MustCompile(`\x1b\[[\d;]*[mK]`)
)

// unitBlock returns the unit block's lines from its header to its `└─` row.
func unitBlock(t *testing.T, text string, header *regexp.Regexp) []string {
	t.Helper()
	start := header.FindStringIndex(text)
	if start == nil {
		t.Fatalf("no block header:\n%s", text)
	}
	end := frameEnd.FindStringIndex(text[start[0]:])
	if end == nil {
		t.Fatalf("the block has no `└─ HH:MM · <duration>` row:\n%s", text)
	}
	return strings.Split(text[start[0]:start[0]+end[1]], "\n")
}

// checkInsideFrame fails on a block line between the header and the end row
// that is not prefixed with the frame's `│`.
func checkInsideFrame(t *testing.T, block []string) {
	t.Helper()
	for _, line := range block[1 : len(block)-1] {
		if !strings.HasPrefix(line, "│") {
			t.Errorf("a block line is outside the frame: %q\n%s", line, strings.Join(block, "\n"))
		}
	}
}

// spinnerLabels returns every spinner frame the raw TTY output drew.
func spinnerLabels(raw string) []string {
	var labels []string
	for _, segment := range strings.Split(ansiControl.ReplaceAllString(raw, ""), "\r") {
		if match := spinnerFrame.FindStringSubmatch(segment); match != nil {
			labels = append(labels, match[1])
		}
	}
	return labels
}
