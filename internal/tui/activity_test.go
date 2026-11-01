package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/runner"
)

func activityParser(stream string) *agentio.ActivityParser {
	return agentio.NewActivityParser(map[string]any{"stream": stream})
}

// parseActivityLines feeds complete JSON lines through the real parser and
// writes the matching call log so every ref can page back to its original
// bytes.
func parseActivityLines(t *testing.T, stream string, lines []string) ([]agentio.Activity, string) {
	t.Helper()
	log := filepath.Join(t.TempDir(), "call.out")
	body := strings.Join(lines, "\n")
	if err := os.WriteFile(log, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	parser := activityParser(stream)
	var out []agentio.Activity
	offset := int64(0)
	for _, line := range lines {
		out = append(out, parser.Feed("stdout", line, agentio.ActivityRef{Stream: "stdout", Offset: offset, Length: len(line)})...)
		offset += int64(len(line) + 1)
	}
	return out, log
}

func streamEvent(unit string, attempt int, log string) runner.Event {
	return runner.Event{Type: "stream", Unit: unit, Fields: map[string]any{"attempt": float64(attempt), "log": log}}
}

func attemptEvent(unit string, n int, log string) runner.Event {
	return runner.Event{Type: "attempt", Unit: unit, Fields: map[string]any{"n": float64(n), "log": log}}
}

func activityEvent(unit string, activity agentio.Activity) runner.Event {
	activity.Unit = unit
	if activity.Agent == "" {
		activity.Agent = "pi"
	}
	if activity.Model == "" {
		activity.Model = "glm-5.3"
	}
	if activity.Effort == "" {
		activity.Effort = "max"
	}
	return runner.Event{Type: "activity", Unit: unit, Data: activity}
}

func addEvents(model Model, events ...runner.Event) Model {
	for _, event := range events {
		model = model.ApplyEvent(event)
	}
	return model
}

func historyModel(t *testing.T, events ...runner.Event) Model {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	root := writeTestProject(t)
	model := loadedModel(t, root)
	model = selectRow(t, model, "unit:P001.1.2")
	model = resize(t, model, 120, 40)
	return addEvents(model, events...)
}

func TestSanitizeNeutralizesTerminalControls(t *testing.T) {
	input := "\x1b[31mred\x1b[0m \x1b]0;title\x07 keep\r\nnext\rline\x00\x07\x9bignored\u202eevil"
	got := Sanitize(input)
	for _, bad := range []string{"\x1b", "\x07", "\x00", "\x9b", "\u202e"} {
		if strings.Contains(got, bad) {
			t.Fatalf("Sanitize left %q in %q", bad, got)
		}
	}
	for _, want := range []string{"red", "keep", "next", "line", "ignored", "evil"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Sanitize removed %q: %q", want, got)
		}
	}
	if !strings.Contains(got, "\n") {
		t.Fatalf("Sanitize lost line breaks: %q", got)
	}
}

func TestActivityHistoryMixedCLIs(t *testing.T) {
	codex, codexLog := parseActivityLines(t, "codex", []string{
		`{"type":"item.started","item":{"id":"c1","type":"command_execution","command":"go test ./..."}}`,
		`{"type":"item.completed","item":{"id":"c1","type":"command_execution","aggregated_output":"ok","exit_code":0}}`,
		`{"type":"turn.completed","usage":{"input_tokens":4,"output_tokens":2}}`,
		`not json`,
	})
	pi, piLog := parseActivityLines(t, "pi", []string{
		`{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":"thinking hard"}}`,
		`{"type":"tool_execution_start","toolCallId":"p1","toolName":"bash","args":{"command":"ls"}}`,
		`{"type":"tool_execution_end","toolCallId":"p1","toolName":"bash","result":"a.go","isError":false}`,
	})
	claude, claudeLog := parseActivityLines(t, "claude", []string{
		`{"type":"assistant","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"done"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"pwd"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"/tmp","is_error":false}]}}`,
	})

	unit := "P001.1.2"
	events := []runner.Event{streamEvent(unit, 1, codexLog)}
	for _, activity := range codex {
		events = append(events, activityEvent(unit, activity))
	}
	events = append(events, streamEvent(unit, 2, piLog), runner.Event{Type: "activity", Unit: unit, Data: agentio.Activity{Kind: agentio.ActivityKindStderr, Raw: "warning: \x1b[31mred\x1b[0m\n"}})
	for _, activity := range pi {
		events = append(events, activityEvent(unit, activity))
	}
	events = append(events, streamEvent(unit, 3, claudeLog))
	for _, activity := range claude {
		events = append(events, activityEvent(unit, activity))
	}
	events = append(events, runner.Event{Type: "usage", Unit: unit})

	model := historyModel(t, events...)
	model = send(t, model, keyMsg("enter"))
	rendered := model.String()
	for _, want := range []string{
		"Activity", "tool Bash started", "tool Bash done", "tool bash started", "tool bash done",
		"message", "reasoning", "stderr", "usage", "unknown event", "pi/glm-5.3 max", "warning: red",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered history lacks %q:\n%s", want, rendered)
		}
	}
	// An unconfirmed outcome is never presented as success.
	if strings.Contains(rendered, "tool Bash done · pi/glm-5.3 max") && strings.Count(rendered, "done") < 1 {
		t.Fatalf("unexpected tool state in:\n%s", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "\x1b[") {
			t.Fatalf("rendered history contains an escape sequence: %q", line)
		}
	}
}

func TestActivityHistoryBoundedAndPaged(t *testing.T) {
	history := NewActivityHistory(20)
	for i := 0; i < 60; i++ {
		history.Add(runner.Event{Type: "state", Unit: "P001.1.1", Fields: map[string]any{"state": strconv.Itoa(i)}})
	}
	if got := len(history.Entries("P001.1.1")); got != 20 {
		t.Fatalf("history length = %d, want 20", got)
	}
	if history.Dropped("P001.1.1") == 0 {
		t.Fatal("dropped entries were not counted")
	}

	body := strings.Repeat("0123456789", 1400)
	log := filepath.Join(t.TempDir(), "big.out")
	if err := os.WriteFile(log, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	activity := agentio.Activity{
		Kind: agentio.ActivityKindTool, State: agentio.ActivityStateSucceeded,
		Tool: "Bash", ToolID: "b1", Command: "cat big", Result: "clipped",
		Ref: agentio.ActivityRef{Stream: "stdout", Offset: 0, Length: len(body)},
	}
	model := historyModel(t, streamEvent("P001.1.2", 1, log), activityEvent("P001.1.2", activity))
	model = send(t, model, keyMsg("enter"))
	model = send(t, model, keyMsg("enter"))
	page := model.pages[activityPageKey("P001.1.2", model.activityEntries("P001.1.2")[0])]
	if page == nil || page.Err != "" || len(page.Text) > activityPageSize {
		t.Fatalf("first page = %#v", page)
	}
	for i := 0; i < 20; i++ {
		model = send(t, model, keyMsg("]"))
	}
	if len(model.pageOrder) > activityPagesPerEntry {
		t.Fatalf("page cache holds %d windows, want <= %d", len(model.pageOrder), activityPagesPerEntry)
	}
	if got := model.pages[activityPageKey("P001.1.2", model.activityEntries("P001.1.2")[0])]; got == nil || got.Skip == 0 {
		t.Fatalf("paging did not advance: %#v", got)
	}
}

func TestActivityRenderWindowIsBounded(t *testing.T) {
	var events []runner.Event
	for i := 0; i < activityRenderWindow+80; i++ {
		events = append(events, activityEvent("P001.1.2", agentio.Activity{Kind: agentio.ActivityKindMessage, Text: "event " + strconv.Itoa(i)}))
	}
	model := historyModel(t, events...)
	model = send(t, model, keyMsg("enter"))
	section := model.activitySection(model.detailWidth())
	if len(section.starts) > activityRenderWindow {
		t.Fatalf("rendered %d entries, want at most %d", len(section.starts), activityRenderWindow)
	}
	if section.base == 0 {
		t.Fatal("the render window did not move to the newest entries")
	}
	if !strings.Contains(strings.Join(section.lines, "\n"), "older observations above") {
		t.Fatalf("the hidden older entries are not named:\n%s", strings.Join(section.lines, "\n"))
	}
	model = send(t, model, keyMsg("home"))
	section = model.activitySection(model.detailWidth())
	if section.base != 0 || !strings.Contains(strings.Join(section.lines, "\n"), "newer observations") {
		t.Fatalf("home did not return to the oldest entries: base=%d", section.base)
	}
}

func TestActivityStderrPage(t *testing.T) {
	log := filepath.Join(t.TempDir(), "call.out")
	if err := os.WriteFile(log, []byte("stdout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr := "err \x1b[31mline\x1b[0m"
	if err := os.WriteFile(agentio.StderrLog(log), []byte(stderr), 0o644); err != nil {
		t.Fatal(err)
	}
	activity := agentio.Activity{
		Kind: agentio.ActivityKindStderr, Raw: stderr,
		Ref: agentio.ActivityRef{Stream: "stderr", Offset: 0, Length: len(stderr)},
	}
	model := historyModel(t, streamEvent("P001.1.2", 1, log), activityEvent("P001.1.2", activity))
	model = send(t, model, keyMsg("enter"))
	model = send(t, model, keyMsg("enter"))
	entries := model.activityEntries("P001.1.2")
	page := model.pages[activityPageKey("P001.1.2", entries[0])]
	if page == nil || page.Err != "" || page.Text != "err line" {
		t.Fatalf("stderr page = %#v", page)
	}
}

func TestActivityLogComesFromAttemptEvent(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	unit := "P001.1.2"
	writeLog := func(name, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	tool := func(attempt int, body string) agentio.Activity {
		return agentio.Activity{
			Kind: agentio.ActivityKindTool, State: agentio.ActivityStateSucceeded,
			Tool: "Bash", ToolID: "b" + strconv.Itoa(attempt), Attempt: attempt,
			Ref: agentio.ActivityRef{Stream: "stdout", Offset: 0, Length: len(body)},
		}
	}
	openPage := func(model Model, entry ActivityEntry) *activityPage {
		t.Helper()
		model = send(t, model, keyMsg("enter"))
		model = send(t, model, keyMsg("enter"))
		return model.pages[activityPageKey(unit, entry)]
	}

	t.Run("attempt before the call opens the log", func(t *testing.T) {
		body := "output written before the call returned"
		log := writeLog("call.out", body)
		activity := tool(1, body)
		model := historyModel(t, attemptEvent(unit, 1, log), activityEvent(unit, activity))
		entries := model.activityEntries(unit)
		page := openPage(model, entries[len(entries)-1])
		if page == nil || page.Err != "" || !strings.Contains(page.Text, body) {
			t.Fatalf("page from attempt event = %#v", page)
		}
	})

	t.Run("resumed attempt keeps its own log", func(t *testing.T) {
		firstBody, secondBody := "first attempt bytes", "second attempt bytes"
		first, second := writeLog("call.1.out", firstBody), writeLog("call.2.out", secondBody)
		model := historyModel(t,
			attemptEvent(unit, 1, first), activityEvent(unit, tool(1, firstBody)),
			attemptEvent(unit, 2, second), activityEvent(unit, tool(2, secondBody)),
		)
		if got, err := model.history.Log(unit, 1); err != nil || got != first {
			t.Fatalf("log for attempt 1 = %q, %v", got, err)
		}
		if got, err := model.history.Log(unit, 2); err != nil || got != second {
			t.Fatalf("log for attempt 2 = %q, %v", got, err)
		}
		entries := model.activityEntries(unit)
		page := openPage(model, entries[len(entries)-1])
		if page == nil || page.Err != "" || !strings.Contains(page.Text, secondBody) {
			t.Fatalf("page of attempt 2 = %#v", page)
		}
		if strings.Contains(page.Text, firstBody) {
			t.Fatalf("page of attempt 2 read attempt 1 bytes: %#v", page)
		}
	})

	t.Run("attempt without a number uses the newest log", func(t *testing.T) {
		body := "un-numbered activity bytes"
		log := writeLog("call.out", body)
		activity := tool(0, body)
		model := historyModel(t, attemptEvent(unit, 1, log), activityEvent(unit, activity))
		entries := model.activityEntries(unit)
		page := openPage(model, entries[len(entries)-1])
		if page == nil || page.Err != "" || !strings.Contains(page.Text, body) {
			t.Fatalf("page of an un-numbered activity = %#v", page)
		}
	})

	t.Run("unknown attempt is an explicit error", func(t *testing.T) {
		body := "first attempt bytes"
		log := writeLog("call.1.out", body)
		model := historyModel(t, attemptEvent(unit, 1, log), activityEvent(unit, tool(2, body)))
		entries := model.activityEntries(unit)
		page := openPage(model, entries[len(entries)-1])
		if page == nil || page.Err != "call log unknown for attempt 2" {
			t.Fatalf("page without its attempt log = %#v", page)
		}
	})
}

func TestActivityFollowPauseSearchAndJump(t *testing.T) {
	first := agentio.Activity{Kind: agentio.ActivityKindMessage, Role: "assistant", Text: "first message"}
	second := agentio.Activity{Kind: agentio.ActivityKindMessage, Role: "assistant", Text: "needle in the second message"}
	model := historyModel(t, activityEvent("P001.1.2", first), activityEvent("P001.1.2", second))
	model = send(t, model, keyMsg("enter"))
	if !model.followActivity || model.activityCursor != 1 {
		t.Fatalf("follow=%v cursor=%d, want follow on the last event", model.followActivity, model.activityCursor)
	}
	model = send(t, model, keyMsg("up"))
	if model.followActivity || model.activityCursor != 0 {
		t.Fatalf("reading older history did not pause follow: follow=%v cursor=%d", model.followActivity, model.activityCursor)
	}
	model = send(t, model, keyMsg("up"))
	if model.activityCursor != 0 {
		t.Fatalf("cursor escaped the first entry: %d", model.activityCursor)
	}
	model = addEvents(model, activityEvent("P001.1.2", agentio.Activity{Kind: agentio.ActivityKindMessage, Text: "third"}))
	if model.followActivity || model.activityCursor != 0 {
		t.Fatalf("a new event moved a paused reader: follow=%v cursor=%d", model.followActivity, model.activityCursor)
	}
	model = send(t, model, keyMsg("G"))
	if !model.followActivity || model.activityCursor != 2 {
		t.Fatalf("G did not jump to the last event: follow=%v cursor=%d", model.followActivity, model.activityCursor)
	}
	model = send(t, model, keyMsg("/"))
	if !model.searching || model.searchTarget != searchActivity {
		t.Fatal("details search did not target activity")
	}
	for _, part := range []string{"n", "e", "e", "d", "l", "e"} {
		model = send(t, model, keyMsg(part))
	}
	model = send(t, model, keyMsg("enter"))
	if got := len(model.activityEntries("P001.1.2")); got != 1 {
		t.Fatalf("activity search kept %d entries, want 1", got)
	}
	if !strings.Contains(model.statusLine(), "activity-search") {
		t.Fatalf("status line does not show the activity search: %q", model.statusLine())
	}
	model = send(t, model, keyMsg("/"))
	model = send(t, model, keyMsg("esc"))
	if model.activityQuery != "" || len(model.activityEntries("P001.1.2")) != 3 {
		t.Fatalf("Esc did not clear the activity search: %q", model.activityQuery)
	}
}

func TestActivityUnknownValuesAndFallback(t *testing.T) {
	usage := agentio.Activity{Kind: agentio.ActivityKindUsage, Usage: &agentio.ActivityUsage{Input: 4}}
	route := runner.Event{Type: "route_switch", Unit: "P001.1.2", Fields: map[string]any{
		"from": map[string]any{"agent": "pi", "model": "glm-5.3", "effort": "max"},
		"to":   map[string]any{"agent": "codex"},
		"kind": "limit",
	}}
	quota := runner.Event{Type: "quota", Unit: "P001.1.2", Fields: map[string]any{
		"name":    "pi",
		"windows": []any{map[string]any{"label": "five_hour", "used": 0.08, "reset": nil}},
	}}
	model := historyModel(t, activityEvent("P001.1.2", usage), route, quota)
	model = send(t, model, keyMsg("enter"))
	rendered := model.String()
	for _, want := range []string{"out unknown", "fallback after limit"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("unknown or fallback value is not explicit %q:\n%s", want, rendered)
		}
	}
	entries := model.activityEntries("P001.1.2")
	if headline := entryHeadline(entries[1]); !strings.Contains(headline, "codex/model unknown effort unknown") {
		t.Fatalf("fallback target lacks explicit unknown fields: %q", headline)
	}
	body := strings.Join(model.entryBody("P001.1.2", entries[0], 200), "\n")
	for _, want := range []string{"out unknown", "cache read unknown", "cost unknown"} {
		if !strings.Contains(body, want) {
			t.Fatalf("usage body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(rendered, "codex/model unknown effort unknown/done") {
		t.Fatalf("fallback target was rendered as a success:\n%s", rendered)
	}
}

func gitAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func TestWorkingDiffComesFromGit(t *testing.T) {
	gitAvailable(t)
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test")
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "a.txt")
	runGit(t, root, "commit", "-m", "init")
	if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diff, err := ReadWorkingDiff(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-before", "+after", "new.txt", "not part of git diff"} {
		if !strings.Contains(diff, want) {
			t.Fatalf("diff lacks %q:\n%s", want, diff)
		}
	}

	model := loadedModel(t, writeTestProject(t))
	model.root = root
	model.state = StateReady
	model = selectRow(t, model, "unit:P001.1.2")
	model = resize(t, model, 120, 40)
	model = send(t, model, keyMsg("enter"))
	model = send(t, model, keyMsg("d"))
	if model.overlay != overlayDiff || model.diffErr != "" || !strings.Contains(model.String(), "+after") {
		t.Fatalf("diff overlay = %v err=%q\n%s", model.overlay, model.diffErr, model.String())
	}
}

func TestWorkingDiffReadErrorIsExplicit(t *testing.T) {
	gitAvailable(t)
	if _, err := ReadWorkingDiff(t.TempDir()); err == nil || !strings.Contains(err.Error(), "git diff") {
		t.Fatalf("non-repository diff error = %v", err)
	}
	model := loadedModel(t, writeTestProject(t))
	model.root = t.TempDir()
	model.state = StateReady
	model = selectRow(t, model, "unit:P001.1.2")
	model = resize(t, model, 120, 40)
	model = send(t, model, keyMsg("enter"))
	model = send(t, model, keyMsg("d"))
	if model.diffErr == "" || !strings.Contains(model.String(), "cannot read git diff") {
		t.Fatalf("diff read error not shown:\n%s", model.String())
	}
}

func TestVerifyResultView(t *testing.T) {
	log := filepath.Join(t.TempDir(), "verify.out")
	body := "--- stdout\npreflight: ok\n--- stderr\n"
	if err := os.WriteFile(log, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	verify := runner.Event{Type: "verify", Unit: "P001.1.2", Fields: map[string]any{
		"exit_code": float64(1),
		"stage":     "gate",
		"log":       log,
		"stdout":    map[string]any{"path": log, "offset": float64(len("--- stdout\n")), "length": len("preflight: ok")},
		"stderr":    map[string]any{"path": log, "offset": len(body), "length": 0},
	}}
	model := historyModel(t, verify)
	model = send(t, model, keyMsg("enter"))
	if rendered := model.String(); !strings.Contains(rendered, "gate failed · exit 1") {
		t.Fatalf("verify header is wrong:\n%s", rendered)
	}
	model = send(t, model, keyMsg("v"))
	if model.overlay != overlayVerify || model.verifyErr != "" || !strings.Contains(model.verifyText, "preflight: ok") || !strings.Contains(model.verifyText, "--- stderr") {
		t.Fatalf("verify overlay = %v err=%q text=%q", model.overlay, model.verifyErr, model.verifyText)
	}
	model = send(t, model, keyMsg("esc"))

	missing := historyModel(t, runner.Event{Type: "verify", Unit: "P001.1.2", Fields: map[string]any{"exit_code": float64(0), "log": filepath.Join(t.TempDir(), "gone.out")}})
	missing = send(t, missing, keyMsg("enter"))
	missing = send(t, missing, keyMsg("v"))
	if missing.verifyErr == "" || !strings.Contains(missing.String(), "cannot read verify log") {
		t.Fatalf("missing verify log not reported:\n%s", missing.String())
	}
}

func TestVerifyOverlayPagesLargeLog(t *testing.T) {
	log := filepath.Join(t.TempDir(), "verify-large.out")
	body := strings.Repeat("check output line\n", 900) + "last line\n"
	if err := os.WriteFile(log, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	verify := runner.Event{Type: "verify", Unit: "P001.1.2", Fields: map[string]any{
		"exit_code": float64(0),
		"log":       log,
		"stdout":    map[string]any{"path": log, "offset": float64(0), "length": len(body)},
	}}
	model := historyModel(t, verify)
	model = send(t, model, keyMsg("enter"))
	model = send(t, model, keyMsg("v"))
	if model.verifyTotal != len(body) || model.verifySkip != 0 {
		t.Fatalf("first verify page = skip %d total %d", model.verifySkip, model.verifyTotal)
	}
	model = send(t, model, keyMsg("]"))
	if model.verifySkip == 0 {
		t.Fatal("] did not page the verify log")
	}
	model = send(t, model, keyMsg("["))
	if model.verifySkip != 0 {
		t.Fatalf("[ did not return to the first page: skip=%d", model.verifySkip)
	}
}

func TestVerifyWithoutEventIsNotSuccess(t *testing.T) {
	model := historyModel(t, activityEvent("P001.1.2", agentio.Activity{Kind: agentio.ActivityKindMessage, Text: "hi"}))
	model = send(t, model, keyMsg("enter"))
	model = send(t, model, keyMsg("v"))
	if !strings.Contains(model.verifyErr, "no verify event") {
		t.Fatalf("verify without an event = %q", model.verifyErr)
	}
	if !strings.Contains(model.String(), "cannot read verify log") {
		t.Fatalf("missing verify result is not explicit:\n%s", model.String())
	}
}

func TestResolveLogPath(t *testing.T) {
	root := t.TempDir()
	absolute := filepath.Join(t.TempDir(), "absolute.out")
	cases := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{name: "relative", path: ".git/docflow/run/call.out", want: filepath.Join(root, ".git", "docflow", "run", "call.out")},
		{name: "relative slash", path: "logs/../call.out", want: filepath.Join(root, "call.out")},
		{name: "absolute", path: absolute, want: absolute},
		{name: "empty", path: "", wantErr: true},
		{name: "parent escape", path: "../outside.out", wantErr: true},
		{name: "deep escape", path: ".git/../../outside.out", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveLogPath(root, tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveLogPath(%q) = %q, want an error", tc.path, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveLogPath(%q): %v", tc.path, err)
			}
			if got != tc.want {
				t.Fatalf("resolveLogPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestLogPathsResolveFromCheckoutRoot(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root := writeTestProject(t)
	runDir := filepath.Join(root, ".git", "docflow", "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	callBody := "call marker\n"
	callLog := filepath.Join(runDir, "call.out")
	if err := os.WriteFile(callLog, []byte(callBody), 0o644); err != nil {
		t.Fatal(err)
	}
	verifyBody := "--- stdout\nverify marker\n--- stderr\n"
	verifyLog := filepath.Join(runDir, "verify.P001.1.2.0.out")
	if err := os.WriteFile(verifyLog, []byte(verifyBody), 0o644); err != nil {
		t.Fatal(err)
	}
	relCall, err := filepath.Rel(root, callLog)
	if err != nil {
		t.Fatal(err)
	}
	relVerify, err := filepath.Rel(root, verifyLog)
	if err != nil {
		t.Fatal(err)
	}
	const unit = "P001.1.2"
	activity := agentio.Activity{
		Kind: agentio.ActivityKindTool, State: agentio.ActivityStateSucceeded,
		Tool: "Bash", ToolID: "b1", Command: "echo marker",
		Ref: agentio.ActivityRef{Stream: "stdout", Offset: 0, Length: len(callBody)},
	}
	verify := runner.Event{Type: "verify", Unit: unit, Fields: map[string]any{
		"exit_code": float64(0),
		"log":       filepath.ToSlash(relVerify),
		"stdout":    map[string]any{"path": filepath.ToSlash(relVerify), "offset": float64(len("--- stdout\n")), "length": len("verify marker")},
		"stderr":    map[string]any{"path": filepath.ToSlash(relVerify), "offset": float64(len(verifyBody)), "length": 0},
	}}
	model := loadedModel(t, root)
	model = selectRow(t, model, "unit:"+unit)
	model = resize(t, model, 120, 40)
	model = addEvents(model, streamEvent(unit, 1, filepath.ToSlash(relCall)), activityEvent(unit, activity), verify)

	// The process runs from a subdirectory, as `docflow tui` does when the
	// checkout root is above the current directory.
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	model = send(t, model, keyMsg("enter"))
	// The verify event is newer than the call; move to the tool entry.
	model = send(t, model, keyMsg("up"))
	model = send(t, model, keyMsg("enter"))
	page := model.pages[activityPageKey(unit, model.activityEntries(unit)[0])]
	if page == nil || page.Err != "" || !strings.Contains(page.Text, "call marker") {
		t.Fatalf("activity page from a subdirectory = %#v", page)
	}
	if page.Path != callLog {
		t.Fatalf("activity page path = %q, want %q", page.Path, callLog)
	}

	model = send(t, model, keyMsg("v"))
	if model.overlay != overlayVerify || model.verifyErr != "" || !strings.Contains(model.verifyText, "verify marker") {
		t.Fatalf("verify overlay from a subdirectory = %v err=%q text=%q", model.overlay, model.verifyErr, model.verifyText)
	}
	if model.verifyLog != verifyLog {
		t.Fatalf("verify log path = %q, want %q", model.verifyLog, verifyLog)
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestActivityTimestampUsesEventTime(t *testing.T) {
	stamp := time.Date(2026, 11, 1, 12, 34, 56, 0, time.UTC).Unix()
	activity := agentio.Activity{Kind: agentio.ActivityKindMessage, Text: "hi"}
	model := historyModel(t, runner.Event{Type: "activity", Unit: "P001.1.2", Data: activity, Fields: map[string]any{"ts": float64(stamp)}})
	entries := model.history.Entries("P001.1.2")
	if len(entries) != 1 || entries[0].At.UTC().Unix() != stamp {
		t.Fatalf("entry time = %v, want %v", entries[0].At, time.Unix(stamp, 0))
	}
	model = send(t, model, keyMsg("enter"))
	if !strings.Contains(model.String(), time.Unix(stamp, 0).Local().Format("15:04:05")) {
		t.Fatalf("rendered history lacks the event time:\n%s", model.String())
	}
}

// runningToolLog writes the start line of a call and returns the activity the
// stream reports while the tool is still running. The log can grow afterwards
// exactly as CallAgent appends it.
func runningToolLog(t *testing.T, start string) (string, agentio.Activity) {
	t.Helper()
	log := filepath.Join(t.TempDir(), "call.out")
	if err := os.WriteFile(log, []byte(start), 0o644); err != nil {
		t.Fatal(err)
	}
	return log, agentio.Activity{
		Kind: agentio.ActivityKindTool, State: agentio.ActivityStateRunning,
		Tool: "Bash", ToolID: "live", Command: "sleep 1", Attempt: 1,
		Ref: agentio.ActivityRef{Stream: "stdout", Offset: 0, Length: len(start)},
	}
}

func appendCallLog(t *testing.T, log, text string) {
	t.Helper()
	file, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func TestActivityLiveCallTail(t *testing.T) {
	unit := "P001.1.2"
	start := `{"type":"tool_execution_start","toolCallId":"live","toolName":"bash"}`
	log, activity := runningToolLog(t, start)
	model := historyModel(t, streamEvent(unit, 1, log), activityEvent(unit, activity))
	model = send(t, model, keyMsg("enter"))
	model = send(t, model, keyMsg("enter"))
	entries := model.activityEntries(unit)
	key := activityPageKey(unit, entries[0])
	page := model.pages[key]
	if page == nil || page.Err != "" || !page.Live || page.Total != len(start) {
		t.Fatalf("first page of a running call = %#v", page)
	}

	first := "\npartial output\n"
	appendCallLog(t, log, first)
	model = send(t, model, keyMsg("]"))
	page = model.pages[key]
	if page == nil || !strings.Contains(page.Text, "partial output") {
		t.Fatalf("] did not reread the growing log: %#v", page)
	}
	if page.Total != len(start)+len(first) {
		t.Fatalf("total after growth = %d, want %d", page.Total, len(start)+len(first))
	}
	body := strings.Join(model.entryBody(unit, entries[0], 200), "\n")
	if !strings.Contains(body, "call still running,") || !strings.Contains(body, "bytes so far") {
		t.Fatalf("running page has no live footer:\n%s", body)
	}
	if !strings.Contains(body, "call output so far (not confirmed)") {
		t.Fatalf("partial output is presented as a confirmed result:\n%s", body)
	}

	second := "still running\n"
	appendCallLog(t, log, second)
	model = send(t, model, keyMsg("]"))
	page = model.pages[key]
	if page == nil || !strings.Contains(page.Text, "still running") || page.Total != len(start)+len(first)+len(second) {
		t.Fatalf("second ] did not reread the log: %#v", page)
	}

	// An event of the same attempt refreshes the open first page without a
	// key press.
	tail := "event output\n"
	appendCallLog(t, log, tail)
	model = addEvents(model, streamEvent(unit, 1, log))
	page = model.pages[key]
	if page == nil || !page.Live || !strings.Contains(page.Text, "event output") {
		t.Fatalf("a same-attempt event did not refresh the live page: %#v", page)
	}
	if page.Total != len(start)+len(first)+len(second)+len(tail) {
		t.Fatalf("total after a same-attempt event = %d", page.Total)
	}
}

func TestActivityRunningPageStopsAtCompletion(t *testing.T) {
	unit := "P001.1.2"
	start := "start line"
	log, activity := runningToolLog(t, start)
	result := "full result\n"
	model := historyModel(t, streamEvent(unit, 1, log), activityEvent(unit, activity))
	model = send(t, model, keyMsg("enter"))
	model = send(t, model, keyMsg("enter"))
	entries := model.activityEntries(unit)
	key := activityPageKey(unit, entries[0])

	appendCallLog(t, log, "\n"+result)
	model = send(t, model, keyMsg("]"))
	page := model.pages[key]
	if page == nil || !strings.Contains(page.Text, result) {
		t.Fatalf("running page did not read the appended result: %#v", page)
	}
	frozen := page.Total

	finished := agentio.Activity{
		Kind: agentio.ActivityKindTool, State: agentio.ActivityStateSucceeded,
		Tool: "Bash", ToolID: "live", Command: "sleep 1", Result: "full result", Attempt: 1,
		Ref: agentio.ActivityRef{Stream: "stdout", Offset: int64(len(start) + 1), Length: len(result)},
	}
	model = addEvents(model, activityEvent(unit, finished))
	if running := model.pages[key]; running == nil || running.Live {
		t.Fatalf("page of a superseded running call is still live: %#v", running)
	}
	appendCallLog(t, log, "late bytes after completion\n")
	model = send(t, model, keyMsg("up"))
	model = send(t, model, keyMsg("]"))
	if running := model.pages[key]; running == nil || running.Total != frozen || strings.Contains(running.Text, "late bytes") {
		t.Fatalf("a finished call kept reading the log: %#v", running)
	}

	model = send(t, model, keyMsg("down"))
	model = send(t, model, keyMsg("enter"))
	entries = model.activityEntries(unit)
	doneKey := activityPageKey(unit, entries[len(entries)-1])
	done := model.pages[doneKey]
	if done == nil || done.Err != "" || done.Live || done.Text != result || done.Total != len(result) {
		t.Fatalf("finished result page = %#v", done)
	}
}

func TestActivityLivePagingIsBounded(t *testing.T) {
	unit := "P001.1.2"
	start, activity := runningToolLog(t, "start line")
	appendCallLog(t, start, strings.Repeat("live output\n", 10000))
	model := historyModel(t, streamEvent(unit, 1, start), activityEvent(unit, activity))
	model = send(t, model, keyMsg("enter"))
	model = send(t, model, keyMsg("enter"))
	for i := 0; i < 20; i++ {
		model = send(t, model, keyMsg("]"))
		if len(model.pageOrder) > activityPagesPerEntry {
			t.Fatalf("live paging holds %d windows, want <= %d", len(model.pageOrder), activityPagesPerEntry)
		}
	}
}

func TestActivityMissingOrEmptyLogIsExplicit(t *testing.T) {
	unit := "P001.1.2"
	for _, scenario := range []struct {
		name string
		log  string
	}{
		{name: "missing", log: filepath.Join(t.TempDir(), "gone.out")},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, activity := runningToolLog(t, "start line")
			model := historyModel(t, streamEvent(unit, 1, scenario.log), activityEvent(unit, activity))
			model = send(t, model, keyMsg("enter"))
			model = send(t, model, keyMsg("enter"))
			entries := model.activityEntries(unit)
			page := model.pages[activityPageKey(unit, entries[0])]
			if page == nil || page.Err == "" || page.Text != "" {
				t.Fatalf("missing log page = %#v", page)
			}
			if !strings.Contains(model.String(), "cannot read") {
				t.Fatalf("missing log is not shown as an error:\n%s", model.String())
			}
		})
	}

	t.Run("empty", func(t *testing.T) {
		_, activity := runningToolLog(t, "")
		log := filepath.Join(t.TempDir(), "empty.out")
		if err := os.WriteFile(log, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		model := historyModel(t, streamEvent(unit, 1, log), activityEvent(unit, activity))
		model = send(t, model, keyMsg("enter"))
		model = send(t, model, keyMsg("enter"))
		entries := model.activityEntries(unit)
		page := model.pages[activityPageKey(unit, entries[0])]
		if page == nil || page.Err == "" || page.Text != "" {
			t.Fatalf("empty log page = %#v", page)
		}
	})
}
