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

func TestTerminalLayoutUnitResultsAndUnverified(t *testing.T) {
	p, out := newTTYProgress(t)
	p.unit("P001.1.1", "Первый юнит", agents.Route{Agent: "codex", Model: "gpt-6-luna", Effort: "max"})
	p.line("P001.1.1: attempt 1: invalid JSON", true, "")
	p.result("P001.1.1", "done", "abc123 (2 attempts, 4m34s)")
	p.unverified("Проверка на сопряжённых Apple Watch не выполнялась.")
	p.quotas(nil, nil, "", true)
	text := plainTTY(out.String())
	for _, expected := range []string{
		"┌─ P001.1.1: Первый юнит",
		" Codex  │ gpt-6-luna │ max",
		"│  ! attempt 1: invalid JSON",
		"│  ✓ Done: abc123 (2 attempts, 4m34s)",
		"│  ! Unverified:\n│    Проверка на сопряжённых",
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("output does not contain %q:\n%s", expected, text)
		}
	}
	if !strings.HasSuffix(text, "└─\n") {
		t.Errorf("output should end with the unit close, got %q", text)
	}
	if p.block {
		t.Fatal("quotas(finish=true) left the block open")
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
	for _, item := range []struct{ agent, label, effort, colour string }{
		{"codex", "Codex", "low", "90"},
		{"codex", "Codex", "medium", "1;34"},
		{"claude", "Claude", "high", "1;35"},
		{"codex", "Codex", "xhigh", "1;38;5;208"},
		{"claude", "Claude", "max", "1;38;5;208"},
		{"claude", "Claude", "ultra", "1;91"},
	} {
		p.unit("P001.1.1", "Unit", agents.Route{Agent: item.agent, Model: "model", Effort: item.effort})
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
	p.unit("P001.1.1", "replan", agents.Route{Agent: "custom", Model: "model", Effort: "xhigh"})
	p.result("P001.1.1", "replanned", "abc123 (+1 units)")
	p.quotas(nil, nil, "", true)
	text := plainTTY(out.String())
	for _, expected := range []string{"┌─ P001.1.1: replan", " custom  │ model │ xhigh", "↻ Replanned: abc123 (+1 units)"} {
		if !strings.Contains(text, expected) {
			t.Errorf("output does not contain %q:\n%s", expected, text)
		}
	}
}

func TestTerminalFallbackRouteIsShownInUnitBlock(t *testing.T) {
	p, out := newTTYProgress(t)
	p.unit("P001.1.1", "Unit", agents.Route{Agent: "pi", Model: "zai/glm-5.3", Effort: "max"})
	p.line("P001.1.1: pi zai/glm-5.3: limit until 12:30; switching to codex gpt-5.5", false, "")
	p.executor(agents.Route{Agent: "codex", Model: "gpt-5.5"})
	p.result("P001.1.1", "done", "abc123 (1 attempt via codex gpt-5.5)")
	p.quotas(nil, nil, "", true)
	text := plainTTY(out.String())
	if !strings.Contains(text, " Pi  │ zai/glm-5.3 │ max") || !strings.Contains(text, " Codex  │ gpt-5.5 │ -") || !strings.Contains(text, "via codex gpt-5.5") {
		t.Fatalf("fallback route is missing from the unit block:\n%s", text)
	}
}

func TestTerminalBlockedSeparatesWhiteReasonAndElapsed(t *testing.T) {
	for _, status := range []string{"blocked", "replan blocked"} {
		t.Run(status, func(t *testing.T) {
			p, out := newTTYProgress(t)
			p.unit("P001.1.1", "Unit", agents.Route{Agent: "codex"})
			p.line("P001.1.1: "+status+" (needs plan): первая строка\nвторая: детали", false, "red")
			p.elapsed("P001.1.1", 137)
			p.quotas([]NamedQuota{{Name: "codex", Quota: agentio.Quota{{Label: "5h", Used: .2}}}}, nil, "", true)
			raw, text := out.String(), plainTTY(out.String())
			label := strings.Replace(status, "blocked", "Blocked", 1)
			if !strings.Contains(text, "│  ! "+label+" (needs plan):\n│    первая строка\n│    вторая: детали\n│  Time: 2m17s\n└─ Quota") {
				t.Fatalf("blocked reason or elapsed misplaced:\n%s", text)
			}
			if !strings.Contains(raw, "\x1b[31m! "+label) || !strings.Contains(raw, "\x1b[97mпервая строка") {
				t.Fatalf("blocked heading or reason has the wrong color: %q", raw)
			}
		})
	}
}

func TestElapsedPlainAndNoColor(t *testing.T) {
	var plain bytes.Buffer
	p := newProgress(&plain, &plain, false)
	p.elapsed("P001.1.1", 48)
	if plain.String() != "P001.1.1 time: 48s\n" {
		t.Fatal(plain.String())
	}
	p, out := newTTYProgress(t)
	t.Setenv("NO_COLOR", "1")
	p = newProgress(out, out, false)
	p.unit("P001.1.1", "Unit", agents.Route{Agent: "claude"})
	p.line("P001.1.1: blocked (needs human): причина", false, "red")
	p.elapsed("P001.1.1", 48)
	p.close()
	if strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), "│    причина\n│  Time: 48s\n└─") {
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
	p.unit("P001.1.1", strings.Repeat("Длинное название юнита ", 3), agents.Route{Agent: "codex", Model: "provider/very-long-model-name-for-tests", Effort: "high"})
	p.line("P001.1.1: blocked (needs plan): "+strings.Repeat("длинная причина ", 20), false, "red")
	p.elapsed("P001.1.1", 137)
	p.unverified("projects/hitfm/HitFM/Tools/WatchPlaybackBridge.swift:82")
	p.quotas([]NamedQuota{{Name: "codex", Quota: agentio.Quota{{Label: "five_hour", Used: .97, Reset: 1791904200}, {Label: "seven_day", Used: .62, Reset: 1792476000}}}}, nil, "", true)
	text := plainTTY(out.String())
	for _, line := range strings.Split(text, "\n") {
		if len([]rune(line)) > width-1 {
			t.Errorf("line exceeds terminal width: %d > %d: %q", len([]rune(line)), width-1, line)
		}
	}
	if !strings.Contains(text, "97%") || !strings.Contains(text, "resets") {
		t.Fatalf("quota and reset details missing:\n%s", text)
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
	p.unit("P001.1.1", "Unit", agents.Route{Agent: "codex", Model: "model", Effort: "high"})
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
	p.unit("P001.1.1", "Unit", agents.Route{Agent: "codex", Model: "model", Effort: "max"})
	p.quotas([]NamedQuota{{Name: "codex", Quota: quota}}, map[string]agentio.Quota{"codex": before}, "", true)
	p.summary("run: 1 unit, 1m", []NamedQuota{{Name: "codex", Quota: quota}}, map[string]agentio.Quota{"codex": {{Label: "five_hour", Used: .33, Reset: 100}, {Label: "seven_day", Used: .52, Reset: 200}}})
	text := plainTTY(out.String())
	if !regexp.MustCompile(`5h  [█▏▎▍▌▋▊▉░]+\s+97% \(\+11\)`).MatchString(text) {
		t.Errorf("five-hour quota row missing or wrong:\n%s", text)
	}
	for _, expected := range []string{"(+64)", "(+10)", "Quota · used · Δ run", "\x1b[31m 97%", "\x1b[32m 62%"} {
		if !strings.Contains(out.String(), expected) {
			t.Errorf("output does not contain %q:\n%s", expected, out.String())
		}
	}
	p.quotas([]NamedQuota{{Name: "codex", Quota: before}}, nil, "", true)
	if !strings.Contains(out.String(), "\x1b[33m 86%") {
		t.Errorf("yellow threshold not painted: %q", out.String())
	}
}

func TestTerminalLayoutQuotaUnknownWindowAndClampedBar(t *testing.T) {
	p, out := newTTYProgress(t)
	quota := agentio.Quota{{Label: "five_hour", Used: .02, Reset: 10000}, {Label: "30d", Used: 1.2, Reset: nil}}
	p.quotas([]NamedQuota{{Name: "codex", Quota: quota}}, map[string]agentio.Quota{"codex": {{Label: "five_hour", Used: .97, Reset: 100}}}, "", true)
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
	p.unit("P001.1.1", "Unit", agents.Route{Agent: "claude", Model: "opus", Effort: "high"})
	p.result("P001.1.1", "done", "abc123")
	p.quotas([]NamedQuota{{Name: "claude", Quota: agentio.Quota{{Label: "five_hour", Used: .97, Reset: nil}}}}, nil, "", true)
	text := out.String()
	if strings.Contains(text, "\x1b") || !strings.Contains(text, "┌─") || !strings.Contains(text, " Claude ") || !strings.Contains(text, "97%") {
		t.Fatalf("NO_COLOR changed layout or left ANSI codes: %q", text)
	}
}

func TestTerminalLayoutDumbTerminalKeepsPlainOutput(t *testing.T) {
	t.Setenv("TERM", "dumb")
	out := &fakeTTY{}
	p := newProgress(out, out, false)
	p.unit("P001.1.1", "Unit", agents.Route{Agent: "codex", Model: "model", Effort: "max"})
	p.result("P001.1.1", "done", "abc123")
	p.quotas([]NamedQuota{{Name: "codex", Quota: agentio.Quota{{Label: "five_hour", Used: .97, Reset: nil}}}}, nil, "", false)
	want := "P001.1.1 Unit -> codex | model | max\nP001.1.1 done: abc123\nquota: codex 5h 97%\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestTerminalLayoutRedirectedStderrKeepsOriginalMessage(t *testing.T) {
	out := &fakeTTY{}
	var stderr bytes.Buffer
	p := newProgress(out, &stderr, false)
	p.unit("P001.1.1", "Unit", agents.Route{Agent: "codex", Model: "model", Effort: "high"})
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
	_, _, extra := quotaWindow(window, before, false)
	if strings.Contains(extra, "(") {
		t.Fatalf("a different window contributed a delta: %q", extra)
	}
}

func TestQuotaDeltaFromIdleWindow(t *testing.T) {
	window := agentio.QuotaWindow{Label: "5h", Used: .29, Reset: int64(1792421400)}
	for _, reset := range []any{nil, int64(1792421400 - 18000)} {
		before := agentio.Quota{{Label: "5h", Used: 0, Reset: reset}}
		if _, _, extra := quotaWindow(window, before, false); extra != " (+29)" {
			t.Fatalf("idle window with reset %v: delta %q, want \" (+29)\"", reset, extra)
		}
	}
	before := agentio.Quota{{Label: "5h", Used: .4, Reset: int64(1792421400 - 18000)}}
	if _, _, extra := quotaWindow(window, before, false); extra != "" {
		t.Fatalf("rolled-over window contributed a delta: %q", extra)
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
	p.unit("P001.2.2", "Flags", agents.Route{Agent: "codex"})
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
