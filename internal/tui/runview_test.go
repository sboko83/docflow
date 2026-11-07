package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/runner"

	"charm.land/lipgloss/v2"
)

func liveRunFixture(t *testing.T) Model {
	t.Helper()
	m := historyModel(t)
	at := time.Date(2026, 11, 6, 12, 0, 0, 0, time.Local)
	m.now = func() time.Time { return at.Add(42 * time.Second) }
	m.history.now = func() time.Time { return at }
	unit := "P001.1.2"
	route := map[string]string{"agent": "codex", "model": "model-a", "effort": "high"}
	events := []runner.Event{
		{Type: "run_start", Fields: map[string]any{"plan": "P001"}},
		{Type: "unit_start", Unit: unit, Fields: map[string]any{"route": route}},
		{Type: "attempt", Unit: unit, Fields: map[string]any{"n": 2, "route": route}},
		activityEvent(unit, agentio.Activity{Kind: agentio.ActivityKindReasoning, Text: "Inspect the stream.\nKeep the entire reasoning visible.\n\x1b]0;hostile title\aVerify the result.\x1b[31m"}),
		activityEvent(unit, agentio.Activity{Kind: agentio.ActivityKindMessage, Text: "The full agent message.\nIts second line remains visible."}),
		activityEvent(unit, agentio.Activity{Kind: agentio.ActivityKindTool, ToolID: "tool-1", Tool: "bash", Command: "go test ./...", State: agentio.ActivityStateRunning}),
		activityEvent(unit, agentio.Activity{Kind: agentio.ActivityKindTool, ToolID: "tool-1", Tool: "bash", State: agentio.ActivityStateSucceeded, Result: "PASS", Files: []agentio.FileChange{{Path: "internal/tui/view.go"}}}),
		activityEvent(unit, agentio.Activity{Kind: agentio.ActivityKindUsage, Usage: &agentio.ActivityUsage{Input: 100, Output: 20, Total: 120}}),
		{Type: "verify", Unit: unit, Fields: map[string]any{"exit_code": 0}},
	}
	m = addEvents(m, events...)
	return openStream(t, m)
}

func TestFreshRunClearsPreviousActivity(t *testing.T) {
	m := liveRunFixture(t)
	m.lockCheck = func(string) (int, bool, error) { return 0, false, nil }
	m.runState = runner.StateIdle
	m = useControllers(t, m, newFakeController(runner.StateIdle))
	m.activityQuery = "previous run"
	m.followActivity = false
	m, _, started := m.startRun(false)
	if !started {
		t.Fatalf("start refused: %s", m.notice)
	}
	if got := len(m.history.Entries("P001.1.2")); got != 0 {
		t.Fatalf("fresh run retained %d activity blocks", got)
	}
	if got := len(m.history.Entries("")); got != 0 {
		t.Fatalf("fresh run retained %d run events", got)
	}
	if m.activityQuery != "" || !m.followActivity {
		t.Fatal("fresh run retained previous stream reading state")
	}
}

func TestLiveRunFrames(t *testing.T) {
	m := liveRunFixture(t)
	for _, size := range []struct{ w, h int }{{120, 40}, {60, 20}, {32, 10}} {
		m = resize(t, m, size.w, size.h)
		frame := m.String()
		lines := strings.Split(frame, "\n")
		if len(lines) != size.h {
			t.Fatalf("%dx%d: height %d", size.w, size.h, len(lines))
		}
		for _, line := range lines {
			if lipgloss.Width(line) > size.w || strings.ContainsAny(line, "\x1b\a\x00") {
				t.Fatalf("invalid frame line %q", line)
			}
		}
		if size.w == 32 {
			m = send(t, m, keyMsg("tab"))
			if !strings.Contains(m.String(), "Юниты") || !strings.Contains(m.String(), "commit") {
				t.Fatalf("minimum-size units pane lost the stages:\n%s", m.String())
			}
			continue
		}
		// Only the checkout path varies between machines.
		lines[0] = "docflow tui /project"
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " ")
		}
		got := strings.Join(lines, "\n") + "\n"
		path := filepath.Join("testdata", fmt.Sprintf("live-run-%dx%d.golden", size.w, size.h))
		if os.Getenv("UPDATE_RUN_GOLDEN") == "1" {
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil || got != string(want) {
			t.Fatalf("%s mismatch (%v):\n%s", path, err, got)
		}
	}
}

func TestLiveRunReasoningFoldAndPausedReader(t *testing.T) {
	m := liveRunFixture(t)
	full := strings.Join(m.activitySection(100).lines, "\n")
	for _, want := range []string{"◇ Inspect the stream.", "Keep the entire reasoning visible.", "Verify the result.", "Its second line remains visible.", "✎ internal/tui/view.go"} {
		if !strings.Contains(full, want) {
			t.Fatalf("missing %q:\n%s", want, full)
		}
	}
	if strings.ContainsAny(full, "\x1b\a") || strings.Contains(full, "hostile title") {
		t.Fatalf("unsafe reasoning: %q", full)
	}
	m = send(t, m, keyMsg("t"))
	folded := strings.Join(m.activitySection(100).lines, "\n")
	if !strings.Contains(folded, "◇ Inspect the stream.") || strings.Contains(folded, "Keep the entire reasoning visible.") {
		t.Fatalf("reasoning did not fold:\n%s", folded)
	}
	m = send(t, m, keyMsg("t"))
	if full != strings.Join(m.activitySection(100).lines, "\n") {
		t.Fatal("second t did not restore the full stream")
	}
	m = send(t, m, keyMsg("home"))
	before, cursor := m.streamTop, m.activityCursor
	m = addEvents(m, activityEvent("P001.1.2", agentio.Activity{Kind: agentio.ActivityKindReasoning, Text: "new reasoning"}))
	if m.followActivity || m.streamTop != before || m.activityCursor != cursor {
		t.Fatal("new reasoning moved the paused reader")
	}
	for _, key := range []string{"f", "G"} {
		m = send(t, m, keyMsg(key))
		if !m.followActivity || m.activityCursor != len(m.activityEntries(m.selectedUnit()))-1 {
			t.Fatalf("%s did not follow", key)
		}
	}
}

func TestLiveRunLongBlockScrolling(t *testing.T) {
	m := historyModel(t, activityEvent("P001.1.2", agentio.Activity{Kind: agentio.ActivityKindReasoning, Text: strings.Repeat("reasoning line\n", 80)}))
	m = openStream(t, m)
	tail := m.streamTop
	m = send(t, m, keyMsg("up"))
	if m.followActivity || m.streamTop != tail-1 {
		t.Fatalf("up did not scroll within a long block: %d -> %d", tail, m.streamTop)
	}
	m = send(t, m, keyMsg("home"))
	first := m.streamTop
	m = send(t, m, keyMsg("pgdown"))
	if m.activityCursor != 0 || m.streamTop <= first || m.followActivity {
		t.Fatal("page down skipped the long block")
	}
}

func TestLiveRunOverviewSurvivesEviction(t *testing.T) {
	m := liveRunFixture(t)
	m.history.limit = 2
	for i := 0; i < 5; i++ {
		m = addEvents(m, activityEvent("P001.1.2", agentio.Activity{Kind: agentio.ActivityKindMessage, Text: "later"}))
	}
	o := m.runOverviewNow()
	if o.Tokens != 120 || !o.HasTokens || o.Attempt != 2 || o.Route.Agent != "codex" || !o.Units[1].Stages.Verify {
		t.Fatalf("eviction lost run metadata: %+v", o)
	}
	if d, ok := o.Units[1].Duration(m.now()); !ok || d != 42*time.Second {
		t.Fatalf("duration = %v, %v", d, ok)
	}
	m = historyModel(t, activityEvent("P001.1.2", agentio.Activity{Kind: agentio.ActivityKindUsage, Usage: &agentio.ActivityUsage{}}))
	if !strings.Contains(m.runSummaryLine(), "токены ?") {
		t.Fatal("unknown usage was reported as zero")
	}
}

func TestLiveRunLifecycleAndToolSelection(t *testing.T) {
	m := liveRunFixture(t)
	m.activityCursor, m.followActivity = 5, false
	section := m.activitySection(100)
	if !strings.HasPrefix(section.lines[section.starts[5]], "> ") || section.starts[4] != section.starts[5] {
		t.Fatal("completed tool selection lost its shared cursor marker")
	}
	unit := "P001.1.2"
	m = addEvents(m,
		runner.Event{Type: "route_switch", Unit: unit, Fields: map[string]any{"to": map[string]string{"agent": "pi", "model": "next", "effort": "max"}}},
		runner.Event{Type: "attempt", Unit: unit, Fields: map[string]any{"n": 3, "route": map[string]string{"agent": "pi", "model": "next", "effort": "max"}}},
	)
	o := m.runOverviewNow()
	if o.Attempt != 3 || o.Route.Model != "next" || o.Units[1].Stages.Verify || o.Units[1].Stages.Done {
		t.Fatalf("retry retained previous stages or route: %+v", o)
	}
	m = addEvents(m,
		runner.Event{Type: "verify", Unit: unit, Fields: map[string]any{"exit_code": 1}},
		runner.Event{Type: "unit_end", Unit: unit, Fields: map[string]any{"status": "failed", "seconds": 7.5}},
		runner.Event{Type: "run_end"},
	)
	o = m.runOverviewNow()
	if !o.Units[1].Stages.Verify || o.Units[1].Stages.Done {
		t.Fatal("failed verify was shown as done")
	}
	if d, ok := o.Units[1].Duration(m.now()); !ok || d != 7500*time.Millisecond {
		t.Fatalf("final duration = %v, %v", d, ok)
	}
}
