package runner

import (
	"bytes"
	"io"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/agents"
)

type fakeTTY struct{ bytes.Buffer }

func (fakeTTY) IsTerminal() bool { return true }

func (p *Progress) setColumns(columns int) { p.columns = func() int { return columns } }

func plainTTY(text string) string { return ansiStyleRE.ReplaceAllString(text, "") }

func newTTYProgress(t *testing.T) (*Progress, *fakeTTY) {
	t.Helper()
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "")
	out := &fakeTTY{}
	progress := newProgress(out, out, false)
	progress.setColumns(80)
	return progress, out
}

// blockStart is a fixed block start, so a frame's header row is predictable.
var blockStart = time.Date(2026, 3, 9, 14, 22, 0, 0, time.Local)

// ranFor sets how long the open block ran; unitEnd measures the duration from
// the start the frame recorded.
func ranFor(p *Progress, seconds float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unitStart = time.Now().Add(-time.Duration(seconds * float64(time.Second)))
}

// frameEnd is the row that closes a unit block: end time and duration.
var frameEnd = regexp.MustCompile("(?m)^\u2514\u2500 ([0-9]{2}:[0-9]{2}) \u00b7 (.+)$")

func frameDuration(t *testing.T, text string) string {
	t.Helper()
	match := frameEnd.FindStringSubmatch(text)
	if match == nil {
		t.Fatalf("the frame has no end row:\n%s", text)
	}
	return match[2]
}

func TestTerminalLayoutUnitResultsAndUnverified(t *testing.T) {
	p, out := newTTYProgress(t)
	p.unit("P001.1.1", "Первый юнит", agents.Route{Agent: "codex", Model: "gpt-6-luna", Effort: "max"}, blockStart)
	p.line("P001.1.1: attempt 1: invalid JSON", true, "")
	p.result("P001.1.1", "done", "abc123 (2 attempts, 4m34s)")
	p.unverified("Проверка на сопряжённых Apple Watch не выполнялась.")
	p.testChanges([]string{"a_test.go: -1 lines", "b_test.go: +skip"})
	p.quotas([]NamedQuota{{Name: "codex", Quota: agentio.Quota{{Label: "5h", Used: .3}}}}, nil, "")
	ranFor(p, 48)
	p.unitEnd("P001.1.1")
	text := plainTTY(out.String())
	for _, expected := range []string{
		"┌─ 14:22 · P001.1.1: Первый юнит",
		" Codex  │ gpt-6-luna │ max",
		"│  ! attempt 1: invalid JSON",
		"│  ✓ Done: abc123 (2 attempts, 4m34s)",
		"│  ! Unverified:\n│    Проверка на сопряжённых",
		"│  ! Test changes:\n│    a_test.go: -1 lines\n│    b_test.go: +skip",
		"│  Quota · used  Codex \n│  5h",
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("output does not contain %q:\n%s", expected, text)
		}
	}
	if !strings.HasSuffix(text, "· 48s\n") {
		t.Errorf("the last row is not the block's end row: %q", text)
	}
	if p.block {
		t.Fatal("unitEnd left the block open")
	}
	if strings.Contains(text, "\n│\n") {
		t.Fatalf("unit contains empty spacer rows:\n%s", text)
	}
}

func TestTerminalLayoutAgentBadgesAndReasoningColors(t *testing.T) {
	p, out := newTTYProgress(t)
	for agent, want := range map[string]string{"codex": "1;97;48;2;36;87;166", "claude": "1;97;48;2;193;95;60"} {
		if got := p.badge(agent); got != "\x1b["+want+"m "+agentName(agent)+" \x1b[0m" {
			t.Errorf("%s badge colors: %q", agent, got)
		}
	}
	pi := "\x1b[" + STYLES["pi"] + "m P\x1b[0m\x1b[" + STYLES["pi-i"] + "mi \x1b[0m"
	if got := p.badge("pi"); got != pi {
		t.Errorf("pi badge colors: %q", got)
	}
	if agentName("pi") != "Pi" {
		t.Errorf("pi display name: %q", agentName("pi"))
	}
	if agentName(zaiQuota) != "Z.ai" {
		t.Errorf("z.ai display name: %q", agentName(zaiQuota))
	}
	for _, item := range []struct{ agent, label, effort, colour string }{
		{"codex", "Codex", "low", "90"},
		{"codex", "Codex", "medium", "1;34"},
		{"claude", "Claude", "high", "1;35"},
		{"codex", "Codex", "xhigh", "1;38;5;208"},
		{"claude", "Claude", "max", "1;38;5;208"},
		{"claude", "Claude", "ultra", "1;91"},
	} {
		p.unit("P001.1.1", "Unit", agents.Route{Agent: item.agent, Model: "model", Effort: item.effort}, blockStart)
		text := out.String()
		if !strings.Contains(text, "\x1b["+STYLES[item.agent]+"m "+item.label+" ") {
			t.Errorf("%s badge was not painted: %q", item.agent, text)
		}
		if !strings.Contains(text, "\x1b["+item.colour+"m"+item.effort) {
			t.Errorf("%s reasoning effort was not painted: %q", item.effort, text)
		}
	}
	if STYLES["medium"] == STYLES["high"] || STYLES["medium"] == STYLES["max"] || STYLES["high"] == STYLES["max"] {
		t.Fatal("medium, high and max should use distinct styles")
	}
}

func TestTerminalLayoutReplanAndUnknownAgent(t *testing.T) {
	p, out := newTTYProgress(t)
	p.unit("P001.1.1", "replan", agents.Route{Agent: "custom", Model: "model", Effort: "xhigh"}, blockStart)
	p.result("P001.1.1", "replanned", "abc123 (+1 units)")
	p.quotas(nil, nil, "")
	p.unitEnd("P001.1.1")
	text := plainTTY(out.String())
	for _, expected := range []string{"┌─ 14:22 · P001.1.1: replan", " custom  │ model │ xhigh", "↻ Replanned: abc123 (+1 units)"} {
		if !strings.Contains(text, expected) {
			t.Errorf("output does not contain %q:\n%s", expected, text)
		}
	}
}

func TestTerminalFallbackRouteIsShownInUnitBlock(t *testing.T) {
	p, out := newTTYProgress(t)
	p.unit("P001.1.1", "Unit", agents.Route{Agent: "pi", Model: "zai/glm-5.3", Effort: "max"}, blockStart)
	p.line("P001.1.1: pi zai/glm-5.3: limit until 12:30; switching to codex gpt-5.5", false, "")
	p.executor(agents.Route{Agent: "codex", Model: "gpt-5.5"})
	p.result("P001.1.1", "done", "abc123 (1 attempt via codex gpt-5.5)")
	p.quotas(nil, nil, "")
	p.unitEnd("P001.1.1")
	text := plainTTY(out.String())
	if !strings.Contains(text, " Pi  │ zai/glm-5.3 │ max") || !strings.Contains(text, " Codex  │ gpt-5.5 │ -") || !strings.Contains(text, "via codex gpt-5.5") {
		t.Fatalf("fallback route is missing from the unit block:\n%s", text)
	}
}

func TestTerminalBlockedSeparatesWhiteReasonAndUnitEnd(t *testing.T) {
	for _, status := range []string{"blocked", "replan blocked"} {
		t.Run(status, func(t *testing.T) {
			p, out := newTTYProgress(t)
			p.unit("P001.1.1", "Unit", agents.Route{Agent: "codex"}, blockStart)
			p.line("P001.1.1: "+status+" (needs plan): первая строка\nвторая: детали", false, "red")
			p.quotas([]NamedQuota{{Name: "codex", Quota: agentio.Quota{{Label: "5h", Used: .2}}}}, nil, "")
			ranFor(p, 137)
			p.unitEnd("P001.1.1")
			raw, text := out.String(), plainTTY(out.String())
			label := strings.Replace(status, "blocked", "Blocked", 1)
			if !strings.Contains(text, "│  ! "+label+" (needs plan):\n│    первая строка\n│    вторая: детали\n│  Quota · used  Codex \n│  5h") {
				t.Fatalf("blocked reason or quota row misplaced:\n%s", text)
			}
			if got := frameDuration(t, text); got != "2m17s" {
				t.Errorf("the block's end row names %q, want the block's 2m17s:\n%s", got, text)
			}
			if !strings.Contains(raw, "\x1b[31m! "+label) || !strings.Contains(raw, "\x1b[97mпервая строка") {
				t.Fatalf("blocked heading or reason has the wrong color: %q", raw)
			}
		})
	}
}

// TestTerminalClosePlanBlockClosesAfterQuota guards the closing call block:
// it has no duration row, so its owner ends the frame after the quota rows.
func TestTerminalClosePlanBlockClosesAfterQuota(t *testing.T) {
	p, out := newTTYProgress(t)
	p.unit("P001", "close plan", agents.Route{Agent: "claude", Model: "opus"}, blockStart)
	p.result("P001", "done", "abc123")
	p.quotas([]NamedQuota{{Name: "claude", Quota: agentio.Quota{{Label: "five_hour", Used: .2}}}}, nil, "")
	p.close()
	text := plainTTY(out.String())
	if !strings.Contains(text, "\u2502  Quota \u00b7 used  Claude \n\u2502  5h") || !strings.HasSuffix(text, "\u2514\u2500\n") {
		t.Fatalf("the close block must keep the quota inside and end with the frame bottom:\n%s", text)
	}
}

func TestUnitEndPlainAndNoColor(t *testing.T) {
	var plain bytes.Buffer
	p := newProgress(&plain, &plain, false)
	p.unit("P001.1.1", "Unit", agents.Route{Agent: "codex"}, blockStart)
	ranFor(p, 48)
	p.unitEnd("P001.1.1")
	if !regexp.MustCompile("(?m)^P001\\.1\\.1 time: 48s \\([0-9]{2}:[0-9]{2}:[0-9]{2}–[0-9]{2}:[0-9]{2}:[0-9]{2}\\)$").MatchString(plain.String()) {
		t.Fatalf("text duration row: %q", plain.String())
	}
	p.unitEnd("P001.2.2")
	if strings.Contains(plain.String(), "P001.2.2") {
		t.Fatalf("a block that never opened printed a row: %q", plain.String())
	}
	out := &fakeTTY{}
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "xterm")
	p = newProgress(out, out, false)
	p.setColumns(80)
	p.unit("P001.1.1", "Unit", agents.Route{Agent: "claude"}, blockStart)
	p.line("P001.1.1: blocked (needs human): причина", false, "red")
	ranFor(p, 48)
	p.unitEnd("P001.1.1")
	if strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), "│    причина\n└─ ") || frameDuration(t, out.String()) != "48s" {
		t.Fatal(out.String())
	}
}

func TestTerminalLayoutNarrowTerminalWraps(t *testing.T) {
	p, out := newTTYProgress(t)
	width := 40
	p.columns = func() int { return width }
	p.verbose = true
	p.start("P001", "Длинное название плана "+strings.Repeat("Длинное название плана ", 2), 0, false)
	p.close()
	p.unit("P001.1.1", strings.Repeat("Длинное название юнита ", 3), agents.Route{Agent: "codex", Model: "provider/very-long-model-name-for-tests", Effort: "high"}, blockStart)
	p.line("P001.1.1: blocked (needs plan): "+strings.Repeat("длинная причина ", 20), false, "red")
	p.unverified("projects/hitfm/HitFM/Tools/WatchPlaybackBridge.swift:82")
	p.quotas([]NamedQuota{{Name: "codex", Quota: agentio.Quota{{Label: "five_hour", Used: .97, Reset: 1791904200}, {Label: "seven_day", Used: .62, Reset: 1792476000}}}}, nil, "")
	ranFor(p, 137)
	p.unitEnd("P001.1.1")
	text := plainTTY(out.String())
	for _, line := range strings.Split(text, "\n") {
		if len([]rune(line)) > width-1 {
			t.Errorf("line exceeds terminal width: %d > %d: %q", len([]rune(line)), width-1, line)
		}
	}
	if !strings.Contains(text, "97%") || !strings.Contains(text, "↻") {
		t.Fatalf("quota and inline reset details missing:\n%s", text)
	}
	if strings.Count(text, "длинная") != 20 || strings.Count(text, "причина") != 20 {
		t.Fatalf("wrapped text lost words:\n%s", text)
	}
	if strings.Count(text, "┌─") != 1 || p.block {
		t.Fatalf("unexpected block layout or state:\n%s", text)
	}
}

func TestTerminalLayoutSpinnerStaysInsideUnitBlock(t *testing.T) {
	p, out := newTTYProgress(t)
	p.unit("P001.1.1", "Unit", agents.Route{Agent: "codex", Model: "model", Effort: "high"}, blockStart)
	p.mu.Lock()
	p.block, p.label = true, "P001.1.1 verify"
	p.drawLocked("⠋")
	p.mu.Unlock()
	if !strings.HasSuffix(out.String(), "\r\x1b[K│  ⠋ P001.1.1 verify") {
		t.Fatalf("spinner did not stay inside the block: %q", out.String())
	}
}

func TestTerminalLayoutQuotaBarsThresholdsAndScope(t *testing.T) {
	p, out := newTTYProgress(t)
	before := agentio.Quota{{Label: "five_hour", Used: .86, Reset: 100}, {Label: "seven_day", Used: .60, Reset: 200}}
	quota := agentio.Quota{{Label: "five_hour", Used: .97, Reset: 100}, {Label: "seven_day", Used: .62, Reset: 200}}
	p.unit("P001.1.1", "Unit", agents.Route{Agent: "codex", Model: "model", Effort: "max"}, blockStart)
	p.quotas([]NamedQuota{{Name: "codex", Quota: quota}}, map[string]agentio.Quota{"codex": before}, "")
	p.summary("run: 1 unit, 1m", []NamedQuota{{Name: "codex", Quota: quota}}, map[string]agentio.Quota{"codex": {{Label: "five_hour", Used: .33, Reset: 100}, {Label: "seven_day", Used: .52, Reset: 200}}}, nil)
	text := plainTTY(out.String())
	if !regexp.MustCompile(`5h  [█▏▎▍▌▋▊▉░]+\s+97% \(\+11\)`).MatchString(text) {
		t.Errorf("five-hour quota row missing or wrong:\n%s", text)
	}
	for _, expected := range []string{"(+64)", "(+10)", "Quota · used · Δ run", "\x1b[31m 97%", "\x1b[33m 62%"} {
		if !strings.Contains(out.String(), expected) {
			t.Errorf("output does not contain %q:\n%s", expected, out.String())
		}
	}
	p.quotas([]NamedQuota{{Name: "codex", Quota: before}}, nil, "")
	if !strings.Contains(out.String(), "\x1b[31m 86%") {
		t.Errorf("80%% and over is not painted red: %q", out.String())
	}
}

func TestTerminalQuotaModeRowsAndThresholds(t *testing.T) {
	// "used": under 50% green, 50-79% yellow, 80% and over red. "left" shows
	// 100 - used and mirrors the thresholds and the bar.
	for _, item := range []struct {
		mode    string
		used    float64
		percent string
		style   string
		track   int
	}{
		{agents.QuotaUsed, .49, " 49%", "\x1b[32m", 5},
		{agents.QuotaUsed, .50, " 50%", "\x1b[33m", 5},
		{agents.QuotaUsed, .79, " 79%", "\x1b[33m", 2},
		{agents.QuotaUsed, .80, " 80%", "\x1b[31m", 2},
		{agents.QuotaLeft, .49, " 51%", "\x1b[32m", 4},
		{agents.QuotaLeft, .50, " 50%", "\x1b[33m", 5},
		{agents.QuotaLeft, .79, " 21%", "\x1b[33m", 7},
		{agents.QuotaLeft, .80, " 20%", "\x1b[31m", 8},
	} {
		p, out := newTTYProgress(t)
		p.setDisplay(agents.Display{Quota: item.mode, Resets: true})
		p.unit("P001.1.1", "Unit", agents.Route{Agent: "codex"}, blockStart)
		p.quotas([]NamedQuota{{Name: "codex", Quota: agentio.Quota{{Label: "five_hour", Used: item.used}}}}, nil, "")
		p.unitEnd("P001.1.1")
		raw, text := out.String(), plainTTY(out.String())
		if !strings.Contains(text, "\u2502  Quota \u00b7 "+item.mode+"  Codex \n\u2502  5h  ") {
			t.Errorf("%s at %v: the quota row left the frame:\n%s", item.mode, item.used, text)
		}
		if !strings.Contains(raw, item.style+item.percent) {
			t.Errorf("%s at %v: %q is not painted %s:\n%q", item.mode, item.used, item.percent, item.style, raw)
		}
		row := ""
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, "5h  ") {
				row = line
			}
		}
		if empty := strings.Count(row, "\u2591"); empty != item.track {
			t.Errorf("%s at %v: the bar leaves %d cells, want %d: %q", item.mode, item.used, empty, item.track, row)
		}
	}
}

func TestQuotaLeftModeMirrorsPercentAndDelta(t *testing.T) {
	before := agentio.Quota{{Label: "five_hour", Used: .30}}
	window := agentio.QuotaWindow{Label: "five_hour", Used: .32}
	used, left := quotaViewOf(window, before, false), quotaViewOf(window, before, true)
	if used.percent != "32%" || used.delta != " (+2)" || used.shown != 32 {
		t.Fatalf("used view = %#v", used)
	}
	if left.percent != "68%" || left.delta != " (-2)" || left.shown != 68 {
		t.Fatalf("left view = %#v", left)
	}
	display := agents.Display{Quota: agents.QuotaLeft, Resets: false}
	if got := quotaText(agentio.Quota{window}, before, display, false); got != "5h 68% (-2)" {
		t.Fatalf("text quota line in left mode = %q", got)
	}
}

func TestTerminalQuotaInlineResetSwitch(t *testing.T) {
	quota := agentio.Quota{{Label: "five_hour", Used: .3, Reset: 1791904200}, {Label: "seven_day", Used: .4, Reset: 1792476000}}
	long := regexp.MustCompile("\u21bb [A-Z][a-z]{2} [0-9]{2}:[0-9]{2}")
	for _, resets := range []bool{true, false} {
		p, out := newTTYProgress(t)
		p.setDisplay(agents.Display{Quota: agents.QuotaUsed, Resets: resets})
		p.quotas([]NamedQuota{{Name: "codex", Quota: quota}}, nil, "")
		text := plainTTY(out.String())
		shown := strings.Contains(text, "30% \u21bb") && long.MatchString(text)
		if resets != shown {
			t.Errorf("resets=%v, the inline reset is shown=%v:\n%s", resets, shown, text)
		}
		if strings.Contains(text, " resets ") {
			t.Errorf("a separate reset row survived:\n%s", text)
		}
	}
	var plain bytes.Buffer
	p := newProgress(&plain, &plain, true)
	p.setDisplay(agents.Display{Quota: agents.QuotaUsed, Resets: false})
	p.quotas([]NamedQuota{{Name: "codex", Quota: quota}}, nil, "")
	if strings.Contains(plain.String(), "resets") {
		t.Errorf("resets = false left the reset time in text output: %q", plain.String())
	}
}

func TestTerminalQuotaRowStaysInsideNarrowFrame(t *testing.T) {
	p, out := newTTYProgress(t)
	const width = 30
	p.setColumns(width)
	p.setDisplay(agents.Display{Quota: agents.QuotaUsed, Resets: true})
	quota := agentio.Quota{{Label: "seven_day", Used: .62, Reset: 1792476000}}
	before := map[string]agentio.Quota{"codex": {{Label: "seven_day", Used: .6, Reset: 1792476000}}}
	p.unit("P001.1.1", "Unit", agents.Route{Agent: "codex", Model: "model", Effort: "high"}, blockStart)
	p.quotas([]NamedQuota{{Name: "codex", Quota: quota}}, before, "")
	ranFor(p, 48)
	p.unitEnd("P001.1.1")
	text := plainTTY(out.String())
	resetRow := regexp.MustCompile("(?m)^\u2502  +\u21bb [A-Z][a-z]{2} [0-9]{2}:[0-9]{2}$")
	for _, line := range strings.Split(text, "\n") {
		if len([]rune(line)) > width-1 {
			t.Errorf("line exceeds terminal width: %d > %d: %q", len([]rune(line)), width-1, line)
		}
		if strings.ContainsAny(line, "\u2588\u2591\u21bb") && !strings.HasPrefix(line, "\u2502") {
			t.Errorf("a quota line is outside the frame: %q", line)
		}
	}
	if !strings.Contains(text, "62%") || !strings.Contains(text, "(+2)") || !strings.Contains(text, "\u21bb") {
		t.Fatalf("quota details missing:\n%s", text)
	}
	if !resetRow.MatchString(text) {
		t.Fatalf("the reset is not on its own line under the bar:\n%s", text)
	}
}

func TestTerminalQuotaRowWiderThanFrameKeepsText(t *testing.T) {
	// 20 columns: the row is wider than the frame even without its reset. It
	// is emitted as is, the reset still follows on its own line.
	p, out := newTTYProgress(t)
	p.setColumns(20)
	p.setDisplay(agents.Display{Quota: agents.QuotaUsed, Resets: true})
	before := map[string]agentio.Quota{"codex": {{Label: "seven_day", Used: .6, Reset: 1792476000}}}
	p.unit("P001.1.1", "Unit", agents.Route{Agent: "codex"}, blockStart)
	p.quotas([]NamedQuota{{Name: "codex", Quota: agentio.Quota{{Label: "seven_day", Used: .62, Reset: 1792476000}}}}, before, "")
	p.unitEnd("P001.1.1")
	text := plainTTY(out.String())
	if !regexp.MustCompile("(?m)^\u2502  7d  [\u2588\u2589-\u258f\u2591]+  62% \\(\\+2\\)$").MatchString(text) {
		t.Errorf("the quota row lost text:\n%s", text)
	}
	if strings.Count(text, "\u21bb") != 1 || !regexp.MustCompile("(?m)^\u2502  +\u21bb [A-Z][a-z]{2} [0-9]{2}:[0-9]{2}$").MatchString(text) {
		t.Errorf("the reset is not once on its own line:\n%s", text)
	}
}

func TestTerminalQuotaThreeWindowsJoinWithinWidth(t *testing.T) {
	quota := agentio.Quota{
		{Label: "five_hour", Used: .4, Reset: 1791904200},
		{Label: "seven_day", Used: .62, Reset: 1792476000},
		{Label: "30d", Used: .1, Reset: 1793476000},
	}
	for _, c := range []struct{ columns, rows int }{{160, 1}, {80, 2}} {
		p, out := newTTYProgress(t)
		p.setColumns(c.columns)
		p.setDisplay(agents.Display{Quota: agents.QuotaUsed, Resets: true})
		p.unit("P001.1.1", "Unit", agents.Route{Agent: "codex"}, blockStart)
		p.quotas([]NamedQuota{{Name: "codex", Quota: quota}}, nil, "")
		p.unitEnd("P001.1.1")
		text := plainTTY(out.String())
		rows := 0
		for _, line := range strings.Split(text, "\n") {
			if len([]rune(line)) > c.columns-1 {
				t.Errorf("%d columns: line exceeds the terminal: %q", c.columns, line)
			}
			if strings.Contains(line, "\u2591") {
				rows++
			}
		}
		if rows != c.rows || strings.Count(text, "\u21bb") != 3 {
			t.Errorf("%d columns: %d quota rows, want %d with three inline resets:\n%s", c.columns, rows, c.rows, text)
		}
		for _, s := range []string{"5h  ", " 40% ", "7d  ", " 62% ", "30d  ", " 10% "} {
			if !strings.Contains(text, s) {
				t.Errorf("%d columns: missing %q:\n%s", c.columns, s, text)
			}
		}
	}
}

func TestTerminalLayoutResumeLineIsYellowWithoutABang(t *testing.T) {
	p, out := newTTYProgress(t)
	p.unit("P001.1.1", "Unit", agents.Route{Agent: "codex", Model: "model", Effort: "high"}, blockStart)
	p.resume("↻ attempt 1: time cap 2h reached while active; resuming the session")
	raw := out.String()
	text := plainTTY(raw)
	if !strings.Contains(text, "│  ↻ attempt 1: time cap 2h reached while active; resuming the session") {
		t.Errorf("the resume line is not inside the unit block:\n%s", text)
	}
	if strings.Contains(text, "│  !") {
		t.Errorf("an attempt that resumes carries the problem mark:\n%s", text)
	}
	if !strings.Contains(raw, "\x1b[33m↻") {
		t.Errorf("the resume line is not yellow:\n%q", raw)
	}
}

func TestTerminalLayoutSummaryCarriesCapHints(t *testing.T) {
	p, out := newTTYProgress(t)
	hint := "P001.1.1 needs more than 2h on codex model: split the unit"
	p.summary("run: 1 unit, 2h01m", []NamedQuota{{Name: "codex", Quota: agentio.Quota{{Label: "five_hour", Used: .33, Reset: 100}}}}, nil, []string{hint})
	raw := out.String()
	text := plainTTY(raw)
	if !strings.Contains(text, "│  hint: "+hint) {
		t.Errorf("the summary frame has no hint row:\n%s", text)
	}
	if !strings.Contains(raw, "\x1b[33m"+"hint: ") {
		t.Errorf("the hint is not yellow:\n%q", raw)
	}
	if strings.Index(text, hint) < strings.Index(text, "5h") || !strings.HasSuffix(text, "╰─\n") {
		t.Errorf("the hint sits above the quota or outside the frame:\n%s", text)
	}
}

func TestTerminalLayoutQuotaUnknownWindowAndClampedBar(t *testing.T) {
	p, out := newTTYProgress(t)
	quota := agentio.Quota{{Label: "five_hour", Used: .02, Reset: 10000}, {Label: "30d", Used: 1.2, Reset: nil}}
	p.quotas([]NamedQuota{{Name: "codex", Quota: quota}}, map[string]agentio.Quota{"codex": {{Label: "five_hour", Used: .97, Reset: 100}}}, "")
	text := plainTTY(out.String())
	if strings.Contains(text, "(-95)") || !strings.Contains(text, "30d  ██████████ 120%") || !strings.Contains(text, "2%") {
		t.Fatalf("unexpected reset/window rendering:\n%s", text)
	}
}

func TestTerminalLayoutNoColorKeepsLayout(t *testing.T) {
	out := &fakeTTY{}
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "1")
	p := newProgress(out, out, false)
	p.setColumns(80)
	p.unit("P001.1.1", "Unit", agents.Route{Agent: "claude", Model: "opus", Effort: "high"}, blockStart)
	p.result("P001.1.1", "done", "abc123")
	p.quotas([]NamedQuota{{Name: "claude", Quota: agentio.Quota{{Label: "five_hour", Used: .97, Reset: nil}}}}, nil, "")
	text := out.String()
	if strings.Contains(text, "\x1b") || !strings.Contains(text, "┌─") || !strings.Contains(text, " Claude ") || !strings.Contains(text, "97%") {
		t.Fatalf("NO_COLOR changed layout or left ANSI codes: %q", text)
	}
}

func TestTerminalLayoutDumbTerminalKeepsPlainOutput(t *testing.T) {
	t.Setenv("TERM", "dumb")
	out := &fakeTTY{}
	p := newProgress(out, out, false)
	p.unit("P001.1.1", "Unit", agents.Route{Agent: "codex", Model: "model", Effort: "max"}, blockStart)
	p.result("P001.1.1", "done", "abc123")
	p.quotas([]NamedQuota{{Name: "codex", Quota: agentio.Quota{{Label: "five_hour", Used: .97, Reset: nil}}}}, nil, "")
	want := "P001.1.1 Unit -> codex | model | max\nP001.1.1 done: abc123\nquota: codex 5h 97%\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestTerminalLayoutRedirectedStderrKeepsOriginalMessage(t *testing.T) {
	out := &fakeTTY{}
	var stderr bytes.Buffer
	p := newProgress(out, &stderr, false)
	p.unit("P001.1.1", "Unit", agents.Route{Agent: "codex", Model: "model", Effort: "high"}, blockStart)
	p.line("P001.1.1: attempt 1: invalid JSON", true, "")
	if stderr.String() != "P001.1.1: attempt 1: invalid JSON\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestProgressSpinnerAndClear(t *testing.T) {
	out := &fakeTTY{}
	t.Setenv("TERM", "xterm")
	p := newProgress(out, io.Discard, false)
	p.spin("P001.1.1 0s", nil)
	time.Sleep(250 * time.Millisecond)
	p.stop()
	p.line("done", false, "")
	text := out.String()
	if !strings.Contains(text, "\r\x1b[K") || !strings.Contains(text, "P001.1.1 0s") || !strings.HasSuffix(text, "done\n") {
		t.Fatalf("spinner output missing or not cleared: %q", text)
	}
}

func TestProgressNoTTYHasNoCodesOrSpinner(t *testing.T) {
	var out bytes.Buffer
	p := newProgress(&out, io.Discard, false)
	p.spin("x", nil)
	p.line("done", false, "green")
	p.stop()
	if out.String() != "done\n" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestProgressEveryStylePaints(t *testing.T) {
	names := make([]string, 0, len(STYLES))
	for name := range STYLES {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, style := range names {
		t.Run(style, func(t *testing.T) {
			out := &fakeTTY{}
			t.Setenv("TERM", "xterm")
			t.Setenv("NO_COLOR", "")
			p := newProgress(out, io.Discard, false)
			p.line("x", false, style)
			if !strings.Contains(out.String(), "\x1b[") {
				t.Fatalf("style %s was not painted: %q", style, out.String())
			}
		})
	}
}

func TestProgressNoColor(t *testing.T) {
	out := &fakeTTY{}
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "xterm")
	p := newProgress(out, io.Discard, false)
	p.line("done", false, "green")
	if out.String() != "done\n" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestSameResetSlackAndEpochUnits(t *testing.T) {
	if !sameReset(int64(1792421400), int64(1792421400-59)) {
		t.Fatal("reset times 59 seconds apart should match")
	}
	if !sameReset(float64(1792421400000), int64(1792421400)) {
		t.Fatal("millisecond and second epochs should match")
	}
	if sameReset(int64(1792421400), int64(1792421400+18000)) {
		t.Fatal("reset times five hours apart should not match")
	}
	if sameReset(nil, int64(1792421400)) || !sameReset(nil, nil) {
		t.Fatal("nil reset equality differs")
	}
}

func TestQuotaResetWindowsDoNotCrossMatch(t *testing.T) {
	before := agentio.Quota{{Label: "five_hour", Used: .5, Reset: int64(1000)}}
	window := agentio.QuotaWindow{Label: "seven_day", Used: .7, Reset: int64(1000)}
	if view := quotaViewOf(window, before, false); strings.Contains(view.delta, "(") {
		t.Fatalf("a different window contributed a delta: %q", view.delta)
	}
}

func TestQuotaDeltaFromIdleWindow(t *testing.T) {
	window := agentio.QuotaWindow{Label: "5h", Used: .29, Reset: int64(1792421400)}
	for _, reset := range []any{nil, int64(1792421400 - 18000)} {
		before := agentio.Quota{{Label: "5h", Used: 0, Reset: reset}}
		if view := quotaViewOf(window, before, false); view.delta != " (+29)" {
			t.Fatalf("idle window with reset %v: delta %q, want \" (+29)\"", reset, view.delta)
		}
		if view := quotaViewOf(window, before, true); view.delta != " (-29)" {
			t.Fatalf("left mode with reset %v: delta %q, want \" (-29)\"", reset, view.delta)
		}
	}
	before := agentio.Quota{{Label: "5h", Used: .4, Reset: int64(1792421400 - 18000)}}
	if view := quotaViewOf(window, before, false); view.delta != "" {
		t.Fatalf("rolled-over window contributed a delta: %q", view.delta)
	}
}

// Expected rows come from Python textwrap.wrap(text, width,
// break_on_hyphens=False), which the Python runner uses.
func TestWrapParagraphMatchesTextwrap(t *testing.T) {
	cases := []struct {
		text  string
		width int
		want  []string
	}{
		{"  - human token in place", 30, []string{"  - human token in place"}},
		{"  - human token in place and more words here", 20, []string{"  - human token in", "place and more words", "here"}},
		{"    indented", 8, []string{"indented"}},
		{"    abcdefghij", 8, []string{"    abcd", "efghij"}},
		{"  abcdefghijklmnopqrstuvwxy", 10, []string{"  abcdefgh", "ijklmnopqr", "stuvwxy"}},
		{"   ", 10, []string{""}},
		{"", 10, []string{""}},
		{"a  b   c", 20, []string{"a  b   c"}},
		{"a\tb", 20, []string{"a       b"}},
		{"\tx y", 20, []string{"        x y"}},
		{"ab\tcd\tef", 20, []string{"ab      cd      ef"}},
		{"no\u00a0break here", 9, []string{"no\u00a0break", "here"}},
		{"word word word word word word ", 12, []string{"word word", "word word", "word word"}},
		{"abcdefghijklmnopqrstuvwxyz", 10, []string{"abcdefghij", "klmnopqrst", "uvwxyz"}},
		{"x abcdefghijklmnopqrstuvwxyz y", 10, []string{"x abcdefgh", "ijklmnopqr", "stuvwxyz y"}},
	}
	for _, c := range cases {
		got := wrapParagraph(c.text, c.width)
		if strings.Join(got, "|") != strings.Join(c.want, "|") || len(got) != len(c.want) {
			t.Errorf("wrapParagraph(%q, %d) = %q, want %q", c.text, c.width, got, c.want)
		}
	}
}

func TestSubcommandOutputStaysInsideUnitBlock(t *testing.T) {
	stderr := "docs/plans/P001-x.md:78: error: main.go: file not found\n"
	p, out := newTTYProgress(t)
	p.unit("P001.2.2", "Flags", agents.Route{Agent: "codex"}, blockStart)
	p.output(stderr)
	p.line("P001.2.2: the plan no longer matches the tree; replan the unit, then run again", true, "")
	text := plainTTY(out.String())
	if !strings.Contains(text, "\n│  docs/plans/P001-x.md:78: error: main.go: file not found\n│  ! the plan no longer matches") {
		t.Fatalf("subcommand output left the frame:\n%s", text)
	}

	var plain bytes.Buffer
	p = newProgress(&plain, &plain, false)
	p.output(stderr)
	if plain.String() != stderr {
		t.Fatalf("plain output changed: %q", plain.String())
	}
}

func TestTerminalQuotaColumnsAlignAcrossAgents(t *testing.T) {
	timed := agentio.Quota{{Label: "five_hour", Used: .08, Reset: 1791904200}, {Label: "seven_day", Used: .03, Reset: 1792476000}}
	idle := agentio.Quota{{Label: "five_hour", Used: 0}, {Label: "seven_day", Used: .48, Reset: 1792476000}}
	p, out := newTTYProgress(t)
	p.setColumns(120)
	p.setDisplay(agents.Display{Quota: agents.QuotaUsed, Resets: true})
	p.quotas([]NamedQuota{{Name: "zai", Quota: idle}, {Name: "claude", Quota: timed}}, nil, "")
	columns := []int{}
	for _, line := range strings.Split(plainTTY(out.String()), "\n") {
		if i := strings.Index(line, "7d"); i >= 0 && strings.Contains(line, "5h") {
			columns = append(columns, len([]rune(line[:i])))
		}
	}
	if len(columns) != 2 || columns[0] != columns[1] {
		t.Fatalf("7d columns = %v, want two equal:\n%s", columns, plainTTY(out.String()))
	}
}
