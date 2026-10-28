//go:build darwin || linux

package runner

import (
	"regexp"
	"strings"
	"testing"
)

// Acceptance of P045 on a real PTY: the unit block, the quota inside it, the
// quota display modes from DOCFLOW_AGENTS, and the liveness spinner.

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

func TestAcceptTerminalUnitBlockAndQuotaDisplay(t *testing.T) {
	for _, mode := range []struct {
		name, display, header, percent string
		resets                         bool
	}{
		{"default", "", "Quota · used", "86%", true},
		{"left", "[display]\nquota = \"left\"\nresets = false\n", "Quota · left", "14%", false},
	} {
		t.Run(mode.name, func(t *testing.T) {
			f := newRunFixture(t)
			f.configure("stream = \"claude\"\n" + mode.display)
			t.Setenv("DOCFLOW_AGENTS", f.config)
			f.steps(quotaEvent(.86, .6, 1791904200) + writeReply("a.txt"))
			code, text, _ := runInTerminal(t, f, "--max-units", "1")
			if code != 0 {
				t.Fatalf("exit %d:\n%s", code, text)
			}
			block := unitBlock(t, text, unitHeader)
			checkInsideFrame(t, block)
			quota := regexp.MustCompile(`^│ +5h +[█▏▎▍▌▋▊▉░]+ +` + mode.percent)
			found := false
			for _, line := range block {
				if quota.MatchString(line) {
					found = true
					if strings.Contains(line, "↻") != mode.resets {
						t.Errorf("reset shown = %v, want %v: %q", !mode.resets, mode.resets, line)
					}
				}
			}
			if !found {
				t.Errorf("no 5h quota row with %s inside the unit block:\n%s", mode.percent, strings.Join(block, "\n"))
			}
			if !strings.Contains(text, mode.header) {
				t.Errorf("missing %q:\n%s", mode.header, text)
			}
			if !mode.resets && strings.Contains(text, "↻") {
				t.Errorf("resets = false still prints a reset:\n%s", text)
			}
		})
	}
}

func TestAcceptTerminalSpinnerShowsLiveness(t *testing.T) {
	tool := emit(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{
		"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": "go test ./..."},
	}}}}) + "sleep 2\n" + emit(map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{
		"type": "tool_result", "tool_use_id": "t1", "content": "ok",
	}}}})
	t.Run("wide", func(t *testing.T) {
		f := newRunFixture(t)
		f.configure("stream = \"claude\"\n")
		f.steps(tool + writeReply("a.txt"))
		code, text, raw := runInTerminal(t, f, "--config", f.config, "--max-units", "1")
		if code != 0 {
			t.Fatalf("exit %d:\n%s", code, text)
		}
		label := regexp.MustCompile(`^P001\.1\.1 \S+ · turn 1 · \S+ ago · tool \S+$`)
		for _, l := range spinnerLabels(raw) {
			if label.MatchString(l) {
				return
			}
		}
		t.Fatalf("no spinner frame with turn age and tool time:\n%q", spinnerLabels(raw))
	})
	t.Run("narrow", func(t *testing.T) {
		// 30 columns: the spinner is cut to the line and the quota row's reset
		// moves under the bar; no line may pass the terminal's right edge.
		const width = 30
		f := newRunFixture(t)
		f.configure("stream = \"claude\"\n")
		f.steps(tool + quotaEvent(.86, .6, 1791904200) + writeReply("a.txt"))
		code, text, raw := runInSizedTerminal(t, f, width, "--config", f.config, "--max-units", "1")
		if code != 0 {
			t.Fatalf("exit %d:\n%s", code, text)
		}
		labels := spinnerLabels(raw)
		if len(labels) == 0 {
			t.Fatalf("no spinner frame drawn:\n%q", raw)
		}
		for _, segment := range strings.Split(ansiControl.ReplaceAllString(raw, ""), "\r") {
			for _, line := range strings.Split(segment, "\n") {
				if n := len([]rune(line)); n > width-1 {
					t.Errorf("line exceeds the terminal: %d > %d: %q", n, width-1, line)
				}
			}
		}
		if !regexp.MustCompile(`(?m)^│ +↻ [A-Z][a-z]{2} [0-9]{2}:[0-9]{2}$`).MatchString(text) {
			t.Errorf("the reset did not move under the bar inside the frame:\n%s", text)
		}
	})
}

func TestAcceptTerminalCloseBlock(t *testing.T) {
	// The closing call in a real TTY: the spinner names the close call, the
	// active cap reads as a resumed session, the quota sits inside the close
	// block, and the cap hint sits inside the run summary frame.
	f := readyCloseFixture(t)
	f.configure("stream = \"claude\"\ntimeout = 4\n")
	tick := emit(map[string]any{"type": "assistant", "message": map[string]any{
		"content": []any{map[string]any{"type": "text", "text": "tick"}},
	}})
	active := emit(map[string]any{"type": "system", "subtype": "init", "session_id": "s1"}) + tick +
		"sleep 1\n" + tick + "sleep 1\n" +
		emit(map[string]any{"type": "result", "is_error": false, "result": "working", "session_id": "s1"}) +
		"sleep 60\n"
	f.steps(active, closeArchiveStep(f.binary)+quotaEvent(.86, .6, 1791904200)+doneReply())
	// 120 columns keep the resume and hint lines unwrapped.
	code, text, raw := runInSizedTerminal(t, f, 120, "--config", f.config, "--close", "--attempts", "2")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	closeSpinner := false
	for _, l := range spinnerLabels(raw) {
		if regexp.MustCompile(`^P001 close · turn \d+ · \S+ ago`).MatchString(l) {
			closeSpinner = true
		}
	}
	if !closeSpinner {
		t.Errorf("no close spinner frame with turn and event age:\n%q", spinnerLabels(raw))
	}
	if !regexp.MustCompile(`(?m)│  ↻ close attempt 1: time cap 4s reached while active \(\d+ turns?\); resuming the session$`).MatchString(text) {
		t.Errorf("the active cap is not a resume line inside the close block:\n%s", text)
	}
	if !regexp.MustCompile(`(?m)^│ +hint: P001 needs more than 4s on fake -: set timeout: in the plan header$`).MatchString(text) {
		t.Errorf("the cap hint is not inside the summary frame:\n%s", text)
	}
	if !regexp.MustCompile(`(?m)^┌─ [0-9]{2}:[0-9]{2} · P001: close plan\n(│[^\n]*\n)*│ +5h +[█▏▎▍▌▋▊▉░]+ +86% ↻[^\n]*\n└─$`).MatchString(text) {
		t.Errorf("the closing quota is not inside the close block before its end row:\n%s", text)
	}
}
