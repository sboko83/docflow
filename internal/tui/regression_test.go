package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/runner"
)

func messageEvents(unit, prefix string, n int) []runner.Event {
	var events []runner.Event
	for i := 0; i < n; i++ {
		events = append(events, activityEvent(unit, agentio.Activity{Kind: agentio.ActivityKindMessage, Role: "assistant", Text: fmt.Sprintf("%s-%02d", prefix, i)}))
	}
	return events
}

// TestFollowMovesTheStreamWithoutFocus keeps the stream on the newest
// observation while the tree has focus, and a switch to the stream on a
// narrow terminal shows the newest window at once.
func TestFollowMovesTheStreamWithoutFocus(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	model = selectRow(t, model, "unit:P001.1.2")
	if model.focus != focusTree {
		t.Fatalf("focus = %v, want the tree", model.focus)
	}
	// Follow keeps the stream current while the reader is still on the
	// project screen: switching to it must not show a stale window.
	model = addEvents(model, messageEvents("P001.1.2", "entry", 60)...)
	model = send(t, model, keyMsg("3"))
	rendered := model.String()
	if !strings.Contains(rendered, "entry-59") || strings.Contains(rendered, "entry-00") {
		t.Fatalf("follow left the stream behind while the tree had the focus:\n%s", rendered)
	}

	// The same holds on a narrow terminal, where the stream is the whole
	// screen instead of the details pane.
	narrow := resize(t, loadedModel(t, root), 80, 40)
	narrow = selectRow(t, narrow, "unit:P001.1.2")
	narrow = addEvents(narrow, messageEvents("P001.1.2", "entry", 60)...)
	narrow = send(t, narrow, keyMsg("3"))
	rendered = narrow.String()
	if !strings.Contains(rendered, "entry-59") || strings.Contains(rendered, "entry-00") {
		t.Fatalf("the narrow stream opened a stale window:\n%s", rendered)
	}
}

// TestDialogKeysStayOnSmallScreen keeps the keys that answer a dialog on a
// 40x12 screen and lets the clipped body scroll into view.
func TestDialogKeysStayOnSmallScreen(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 40, 12)
	first := newFakeController(runner.StateIdle)
	first.prompt("confirm_start", map[string]any{"preview": runner.StartReview{
		Plan: "P001", Title: "Кеш", Unit: "P001.1.2", Kind: "unit",
		Permissions: "full", Full: true,
		Prerequisites: []string{"apple watch: `test -d /watch`", "login — needs a person"},
	}})
	model = useControllers(t, model, first)
	model = send(t, model, keyMsg("s"))
	model = poll(t, model)
	if model.dialog != dialogStart {
		t.Fatalf("start dialog = %v", model.dialog)
	}
	rendered := model.String()
	assertScreenFits(t, rendered, 40, 12)
	if !strings.Contains(rendered, "Enter/y starts") || !strings.Contains(rendered, "more lines") {
		t.Fatalf("start dialog lost its keys or the scroll marker at 40x12:\n%s", rendered)
	}
	seen := rendered
	for i := 0; i < 20; i++ {
		model = send(t, model, keyMsg("down"))
		seen += model.String()
	}
	if !strings.Contains(seen, "needs a person") {
		t.Fatalf("scrolling never showed the last prerequisite:\n%s", seen)
	}
	if !strings.Contains(model.String(), "Enter/y starts") || len(first.answers) != 0 {
		t.Fatalf("scrolling answered the dialog or hid its keys: answers=%v\n%s", first.answers, model.String())
	}

	human, _ := humanBlockModel(t, newFakeController(runner.StateIdle))
	// At the minimum size the notice leaves two body lines: the note the
	// person types keeps one of them.
	minimum := resize(t, human, 32, 10)
	assertScreenFits(t, minimum.String(), 32, 10)
	for _, want := range []string{"note: вошёл", "Enter unblocks"} {
		if !strings.Contains(minimum.String(), want) {
			t.Fatalf("human dialog lost %q at 32x10:\n%s", want, minimum.String())
		}
	}
	human = resize(t, human, 40, 12)
	rendered = human.String()
	assertScreenFits(t, rendered, 40, 12)
	for _, want := range []string{"note: вошёл", "Enter unblocks"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("human dialog lost %q at 40x12:\n%s", want, rendered)
		}
	}
	seen = rendered
	for i := 0; i < 20; i++ {
		human = send(t, human, keyMsg("down"))
		seen += human.String()
	}
	if !strings.Contains(seen, "Continue runs") || human.dialogInput != "вошёл" {
		t.Fatalf("human dialog body not reachable or note changed (%q):\n%s", human.dialogInput, seen)
	}
}

// TestHistoryKeepsTheLogOfItsOwnCall reads a pre-pause entry from its own call
// log after a resumed call reused the attempt number.
func TestHistoryKeepsTheLogOfItsOwnCall(t *testing.T) {
	unit := "P001.1.2"
	dir := t.TempDir()
	logA := filepath.Join(dir, "call-a.log")
	logB := filepath.Join(dir, "call-b.log")
	lineA := `{"type":"tool_execution_end","toolCallId":"a","result":"OUTPUT-OF-CALL-A"}`
	lineB := `{"type":"tool_execution_end","toolCallId":"b","result":"OUTPUT-OF-CALL-B"}`
	if err := os.WriteFile(logA, []byte(lineA), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logB, []byte(lineB), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := func(id string, length int) agentio.Activity {
		return agentio.Activity{Kind: agentio.ActivityKindTool, State: agentio.ActivityStateSucceeded,
			Tool: "Bash", ToolID: id, Command: "echo " + id, Attempt: 1,
			Ref: agentio.ActivityRef{Stream: "stdout", Offset: 0, Length: length}}
	}
	model := historyModel(t,
		attemptEvent(unit, 1, logA), activityEvent(unit, tool("a", len(lineA))),
		// The resumed call is attempt 1 again, with a log of its own.
		attemptEvent(unit, 1, logB), activityEvent(unit, tool("b", len(lineB))),
	)
	entries := model.activityEntries(unit)
	cursor := -1
	for i, entry := range entries {
		if entry.Type == "activity" && entry.Activity.ToolID == "a" {
			cursor = i
		}
	}
	if cursor < 0 {
		t.Fatalf("pre-pause entry missing: %#v", entries)
	}
	model = openStream(t, model)
	model.activityCursor = cursor
	model.followActivity = false
	model = send(t, model, keyMsg("enter"))
	page := model.pages[activityPageKey(unit, entries[cursor])]
	if page == nil || page.Path != logA || !strings.Contains(page.Text, "OUTPUT-OF-CALL-A") {
		t.Fatalf("pre-pause entry read %#v, want the log of its own call %s", page, logA)
	}
}

// TestFilteredSelectionFollowsTheNewUnit opens a unit reached through a tree
// filter as a unit reached with the cursor: following its newest entry.
func TestFilteredSelectionFollowsTheNewUnit(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	model = addEvents(model, messageEvents("P001.1.1", "one", 20)...)
	model = selectRow(t, model, "unit:P001.1.2")
	model = addEvents(model, messageEvents("P001.1.2", "two", 8)...)
	model = openStream(t, model)
	for i := 0; i < 3; i++ {
		model = send(t, model, keyMsg("up"))
	}
	if model.followActivity {
		t.Fatal("reading older history did not pause follow")
	}
	model = send(t, model, keyMsg("1"))
	model = send(t, model, keyMsg("/"))
	for _, r := range "Готовый" {
		model = send(t, model, keyMsg(string(r)))
	}
	model = send(t, model, keyMsg("enter"))
	if model.selected != "unit:P001.1.1" {
		t.Fatalf("filter selected %q", model.selected)
	}
	if !model.followActivity || model.activityCursor != 19 {
		t.Fatalf("filtered selection kept the old reader: follow=%v cursor=%d", model.followActivity, model.activityCursor)
	}
	model = addEvents(model, activityEvent("P001.1.1", agentio.Activity{Kind: agentio.ActivityKindMessage, Role: "assistant", Text: "one-LATE"}))
	model = send(t, model, keyMsg("3"))
	if model.activityCursor != 20 || !strings.Contains(model.String(), "one-LATE") {
		t.Fatalf("new events did not move the filtered selection: cursor=%d\n%s", model.activityCursor, model.String())
	}
}
