package tui

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/runner"
)

const testDocflow = `---
docflow: 3
---
## Queue
- P001
- P002

## Frozen

## Backlog
- later work
`

const testPlan = `---
id: P001
title: Кеш и очередь
status: active
created: 2026-11-01
---
## Goal

Сделать кеш быстрым и предсказуемым для длинных путей
и кириллических названий.

## Phase 1: Основы

### [x] P001.1.1 Готовый юнит
Read:
- a.go
Do:
- первое действие
Verify: ` + "`go test ./...`" + `
Depends: -
Commit: P001.1.1: done

### [~] P001.1.2 Текущий юнит с очень длинным названием для проверки ширины
Read:
- b.go
Do:
- раз
- два
- три
Verify: ` + "`go test ./internal/tui/`" + `
Depends: P001.1.1
Handoff:
- не забыть про ширину

## Phase 2: Проверка

### [ ] P001.2.1 Ожидающий юнит
Do:
- сделать

### [!] P001.2.2 Заблокированный юнит
Do:
- ждать человека
Verify: ` + "`true`" + `
`

const draftPlan = `---
id: P002
title: Draft plan
status: draft
created: 2026-11-01
---
## Goal

Draft goal.

## Phase 1: Draft

### [ ] P002.1.1 Draft unit
Do:
- draft
`

func writeTestProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "plans"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "docflow.md"), []byte(testDocflow), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "plans", "P001-cache.md"), []byte(testPlan), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "plans", "P002-draft.md"), []byte(draftPlan), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func loadedModel(t *testing.T, root string) Model {
	t.Helper()
	model := New(root)
	// Test fixtures are directory trees, not git checkouts: the real lock
	// checker would report an unreadable lock and refuse every action. Tests
	// that exercise the lock replace this seam explicitly.
	model.lockCheck = func(string) (int, bool, error) { return 0, false, nil }
	cmd := model.Init()
	if cmd == nil {
		t.Fatal("Init returned no load command")
	}
	updated, _ := model.Update(cmd())
	return updated.(Model)
}

func keyMsg(key string) tea.KeyPressMsg {
	switch key {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	default:
		runes := []rune(key)
		return tea.KeyPressMsg{Text: key, Code: runes[0]}
	}
}

func send(t *testing.T, model Model, msg tea.Msg) Model {
	t.Helper()
	updated, _ := model.Update(msg)
	return updated.(Model)
}

func resize(t *testing.T, model Model, width, height int) Model {
	t.Helper()
	return send(t, model, tea.WindowSizeMsg{Width: width, Height: height})
}

func rowKeys(model Model) []string {
	rows := model.rows()
	keys := make([]string, len(rows))
	for i, row := range rows {
		keys[i] = row.key
	}
	return keys
}

func selectRow(t *testing.T, model Model, key string) Model {
	t.Helper()
	for i := 0; i < len(model.rows())*2; i++ {
		if selected, ok := model.selectedRow(); ok && selected.key == key {
			return model
		}
		model = send(t, model, keyMsg("down"))
	}
	t.Fatalf("row %q not found in %v", key, rowKeys(model))
	return model
}

func TestLoadReadsQueuePlansPhasesAndUnits(t *testing.T) {
	root := writeTestProject(t)
	model := loadedModel(t, root)
	if model.state != StateReady {
		t.Fatalf("state = %v, want ready (%s)", model.state, model.errText)
	}
	keys := rowKeys(model)
	for _, want := range []string{"section:queue", "plan:P001", "phase:P001:1", "unit:P001.1.1", "unit:P001.1.2", "unit:P001.2.2", "plan:P002"} {
		found := false
		for _, key := range keys {
			if key == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing row %q in %v", want, keys)
		}
	}
	view := resize(t, model, 120, 40).String()
	for _, want := range []string{"Кеш и очередь", "P001.1.2", "doing", "draft", "P002"} {
		if !strings.Contains(view, want) {
			t.Errorf("view does not contain %q", want)
		}
	}
}

func TestEnterExpandsPlanAndUnitCardShowsFields(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	model = selectRow(t, model, "plan:P001")
	if !model.isExpanded("plan:P001") {
		t.Fatal("plan starts collapsed")
	}
	model = send(t, model, keyMsg("enter"))
	if model.isExpanded("plan:P001") {
		t.Fatal("Enter did not collapse the plan")
	}
	if strings.Contains(strings.Join(rowKeys(model), ","), "unit:P001.1.1") {
		t.Fatal("collapsed plan still lists units")
	}
	model = send(t, model, keyMsg("enter"))
	if !model.isExpanded("plan:P001") {
		t.Fatal("second Enter did not expand the plan")
	}
	model = selectRow(t, model, "unit:P001.1.2")
	view := model.String()
	for _, want := range []string{"Goal", "Do", "Verify", "Depends", "Handoff", "Сделать кеш", "не забыть про ширину", "Status: doing"} {
		if !strings.Contains(view, want) {
			t.Errorf("unit card does not contain %q", want)
		}
	}
}

func TestTabSwitchesPanelsAndResizeChangesLayout(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	if model.singlePanel() {
		t.Fatal("120 columns should use two panels")
	}
	if model.focus != focusTree {
		t.Fatalf("initial focus = %v", model.focus)
	}
	model = selectRow(t, model, "unit:P001.1.2")
	model = send(t, model, keyMsg("tab"))
	if model.focus != focusDetails {
		t.Fatal("Tab did not move focus to details")
	}
	if !strings.Contains(model.String(), "> Details") {
		t.Fatal("details panel is not marked focused")
	}
	model = resize(t, model, 80, 24)
	if !model.singlePanel() {
		t.Fatal("80 columns should use one panel")
	}
	model = send(t, model, keyMsg("tab"))
	if model.focus != focusTree {
		t.Fatal("Tab did not switch the single panel back to the tree")
	}
	if !strings.Contains(model.String(), "> Tree") {
		t.Fatal("single tree panel is not marked focused")
	}
	model = send(t, model, keyMsg("tab"))
	if !strings.Contains(model.String(), "> Details") || strings.Contains(model.String(), "> Tree") {
		t.Fatal("single panel did not switch to details")
	}
	model = resize(t, model, 40, 12)
	if !model.singlePanel() {
		t.Fatal("40 columns should use one panel")
	}
}

func TestSelectionSurvivesRefresh(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	model = selectRow(t, model, "unit:P001.1.2")
	before, _ := model.selectedRow()
	updated := model.applySnapshot(snapshotMsg{snapshot: mustLoad(t, root)})
	after, ok := updated.selectedRow()
	if !ok || after.key != before.key {
		t.Fatalf("selection after refresh = %q, want %q", after.key, before.key)
	}
}

func mustLoad(t *testing.T, root string) Snapshot {
	t.Helper()
	snapshot, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestSearchFiltersPlansAndUnits(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	model = send(t, model, keyMsg("/"))
	if !model.searching {
		t.Fatal("slash did not start search")
	}
	for _, part := range []string{"з", "а", "б", "л", "о", "к"} {
		model = send(t, model, keyMsg(part))
	}
	model = send(t, model, keyMsg("enter"))
	if model.searching {
		t.Fatal("Enter did not finish search")
	}
	keys := strings.Join(rowKeys(model), ",")
	if !strings.Contains(keys, "unit:P001.2.2") {
		t.Fatalf("search did not keep the blocked unit: %s", keys)
	}
	if strings.Contains(keys, "unit:P001.1.1") {
		t.Fatalf("search kept an unrelated unit: %s", keys)
	}
	if !strings.Contains(model.String(), "search:") {
		t.Fatal("status line does not show the search")
	}
	model = send(t, model, keyMsg("/"))
	model = send(t, model, keyMsg("esc"))
	if model.query != "" || model.searching {
		t.Fatal("Esc did not cancel search")
	}
}

func TestHelpAndDetailScroll(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 80, 24)
	model = send(t, model, keyMsg("?"))
	if !model.help || !strings.Contains(model.String(), "Keyboard and workspace") {
		t.Fatal("? did not open help")
	}
	model = send(t, model, keyMsg("esc"))
	if model.help {
		t.Fatal("Esc did not close help")
	}
	model = selectRow(t, model, "unit:P001.1.2")
	model = send(t, model, keyMsg("enter"))
	if model.focus != focusDetails {
		t.Fatal("Enter on a unit did not open details")
	}
	model = send(t, model, keyMsg("down"))
	if model.detailTop == 0 {
		t.Fatal("details did not scroll")
	}
	model = send(t, model, keyMsg("esc"))
	if model.focus != focusTree {
		t.Fatal("Esc did not return to the tree")
	}
}

func TestLoadingEmptyAndErrorStatesHaveActions(t *testing.T) {
	root := writeTestProject(t)
	loading := resize(t, New(root), 80, 24)
	if !strings.Contains(loading.String(), "Loading plan workspace") || !strings.Contains(loading.String(), "r reload") {
		t.Fatal("loading state lacks a message or action")
	}

	empty := newModel(root, func() (Snapshot, error) { return Snapshot{Root: root}, nil }, true)
	empty = resize(t, empty, 80, 24)
	empty = empty.applySnapshot(snapshotMsg{snapshot: Snapshot{Root: root}})
	if empty.state != StateEmpty {
		t.Fatalf("empty state = %v", empty.state)
	}
	if !strings.Contains(empty.String(), "No plans in the queue") || !strings.Contains(empty.String(), "r reload") {
		t.Fatal("empty state lacks a message or action")
	}

	failed := loadedModel(t, root).applySnapshot(snapshotMsg{err: os.ErrNotExist})
	if failed.state != StateError {
		t.Fatalf("error state = %v", failed.state)
	}
	if !strings.Contains(failed.String(), "Cannot load the plan workspace") || !strings.Contains(failed.String(), "r retry") {
		t.Fatal("error state lacks a message or action")
	}
}

func TestLayoutsFitTerminalAndKeepCyrillic(t *testing.T) {
	root := writeTestProject(t)
	model := loadedModel(t, root)
	model = selectRow(t, model, "unit:P001.1.2")
	for _, size := range []struct{ w, h int }{{120, 40}, {80, 24}, {40, 12}} {
		rendered := resize(t, model, size.w, size.h).String()
		lines := strings.Split(rendered, "\n")
		if len(lines) != size.h {
			t.Fatalf("%dx%d rendered %d lines", size.w, size.h, len(lines))
		}
		for _, line := range lines {
			if got := lipgloss.Width(line); got > size.w {
				t.Fatalf("%dx%d line width = %d: %q", size.w, size.h, got, line)
			}
		}
		if size.w >= 80 && !strings.Contains(rendered, "Кеш") && !strings.Contains(rendered, "Текущий") {
			t.Fatalf("%dx%d lost the Cyrillic content", size.w, size.h)
		}
	}
}

func TestTooSmallAndNOCOLOR(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 20, 6)
	small := model.String()
	if !strings.Contains(small, "Terminal too small") || !strings.Contains(small, "Resize the terminal") {
		t.Fatalf("too-small view = %q", small)
	}
	for _, line := range strings.Split(small, "\n") {
		if lipgloss.Width(line) > 20 {
			t.Fatalf("too-small line wider than 20: %q", line)
		}
	}

	t.Setenv("NO_COLOR", "1")
	plain := resize(t, loadedModel(t, root), 120, 40)
	rendered := plain.String()
	if strings.Contains(rendered, "\x1b[") {
		t.Fatal("NO_COLOR view contains ANSI escapes")
	}
	if !strings.Contains(rendered, "Queue") || !strings.Contains(rendered, "P001.1.1") {
		t.Fatal("NO_COLOR view lost content")
	}
}

func TestLongPathsAreTruncatedSafely(t *testing.T) {
	root := writeTestProject(t)
	model := loadedModel(t, root)
	model.root = "/very/long/path/" + strings.Repeat("сегмент/", 40)
	model.snapshot.Queue[0].Path = "docs/plans/" + strings.Repeat("very-long-segment-", 20) + ".md"
	model = resize(t, model, 80, 24)
	rendered := model.String()
	for _, line := range strings.Split(rendered, "\n") {
		if lipgloss.Width(line) > 80 {
			t.Fatalf("line wider than 80: %q", line)
		}
	}
	if !strings.Contains(rendered, "…") {
		t.Fatal("long path was not visibly truncated")
	}
}

func TestRefreshKeyReloads(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 80, 24)
	updated, cmd := model.Update(keyMsg("r"))
	model = updated.(Model)
	if model.state != StateLoading || cmd == nil || model.notice != "reloading" {
		t.Fatalf("refresh state = %v, cmd nil = %v", model.state, cmd == nil)
	}
	updated, _ = model.Update(cmd())
	model = updated.(Model)
	if model.state != StateReady {
		t.Fatalf("reloaded state = %v", model.state)
	}
}

func TestRunRefusesNonTerminal(t *testing.T) {
	root := t.TempDir()
	err := Run(root, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("Run accepted non-terminal streams")
	}
	for _, want := range []string{"interactive terminal", "docflow status", "docflow run"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, missing %q", err, want)
		}
	}
}

func TestResetUnknownStatusIsNamed(t *testing.T) {
	unit := &format.Unit{ID: "P001.1.1", Title: "test", Status: "?"}
	if got := statusWord(unit.Status); got != "unknown" {
		t.Fatalf("statusWord = %q", got)
	}
}

// fakeController stands in for the runner host: the test decides when the
// run reaches a stable state and what the runner asks the person.
type fakeController struct {
	state        runner.State
	done         chan struct{}
	finished     bool
	events       []runner.Event
	prompts      []Prompt
	checkpointed bool
	err          error

	started, resumed, paused, stopped, stopNow, canceled, closed int
	answers                                                      []runner.Response

	// pauseFinishes ends the fake run at the stable boundary and
	// pauseNoCheckpoint models a pause that failed to persist its state.
	pauseFinishes     bool
	pauseNoCheckpoint bool
}

func newFakeController(state runner.State) *fakeController {
	return &fakeController{state: state, done: make(chan struct{})}
}

func (f *fakeController) Start() error  { f.started++; f.state = runner.StateRunning; return nil }
func (f *fakeController) Resume() error { f.resumed++; f.state = runner.StateRunning; return nil }
func (f *fakeController) Pause() error {
	f.paused++
	f.state = runner.StatePausing
	if !f.pauseNoCheckpoint {
		f.checkpointed = true
	}
	if f.pauseFinishes {
		f.finish(runner.StatePaused)
	}
	return nil
}
func (f *fakeController) CancelStart() error {
	f.canceled++
	f.state = runner.StateStopping
	f.finish(runner.StateFinished)
	return nil
}
func (f *fakeController) StopAfterUnit() error {
	f.stopped++
	f.state = runner.StateStopping
	return nil
}
func (f *fakeController) StopNow() error {
	f.stopNow++
	f.state = runner.StateStopping
	f.finish(runner.StateFinished)
	return nil
}
func (f *fakeController) State() runner.State { return f.state }
func (f *fakeController) Checkpointed() bool  { return f.checkpointed }
func (f *fakeController) NextEvent() (runner.Event, bool) {
	if len(f.events) == 0 {
		return runner.Event{}, false
	}
	event := f.events[0]
	f.events = f.events[1:]
	return event, true
}
func (f *fakeController) NextPrompt() (Prompt, bool) {
	if len(f.prompts) == 0 {
		return Prompt{}, false
	}
	prompt := f.prompts[0]
	f.prompts = f.prompts[1:]
	return prompt, true
}
func (f *fakeController) Finished() bool { return f.finished }
func (f *fakeController) Err() error     { return f.err }
func (f *fakeController) Close()         { f.closed++ }

func (f *fakeController) finish(state runner.State) {
	if f.finished {
		return
	}
	f.finished = true
	f.state = state
	close(f.done)
}

func (f *fakeController) prompt(kind string, fields map[string]any) {
	prompt := Prompt{Kind: kind, Message: "Start the current Queue run?", Fields: fields}
	prompt.answer = func(confirmed bool, note string) {
		f.answers = append(f.answers, runner.Response{Confirmed: confirmed, Note: note})
	}
	f.prompts = append(f.prompts, prompt)
}

func poll(t *testing.T, model Model) Model {
	t.Helper()
	updated, _ := model.Update(hostPollMsg{})
	return updated.(Model)
}

func useControllers(t *testing.T, model Model, controllers ...*fakeController) Model {
	t.Helper()
	next := 0
	model.newController = func(string, string) Controller {
		if next >= len(controllers) {
			t.Fatalf("workspace created controller %d, only %d prepared", next+1, len(controllers))
		}
		controller := controllers[next]
		next++
		return controller
	}
	return model
}

func TestStartDialogShowsPreconditionsAndDeclineRunsNothing(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	first := newFakeController(runner.StateIdle)
	first.prompt("confirm_start", map[string]any{"preview": runner.StartReview{
		Plan: "P001", Title: "Кеш", Unit: "P001.1.2", Kind: "unit",
		Permissions: "full", Full: true,
		Prerequisites: []string{"apple watch: `test -d /watch`", "login — needs a person"},
	}})
	second := newFakeController(runner.StateIdle)
	model = useControllers(t, model, first, second)

	updated, _ := model.Update(keyMsg("s"))
	model = updated.(Model)
	if first.started != 1 || model.runState != runner.StateRunning {
		t.Fatalf("start: started=%d state=%s", first.started, model.runState)
	}
	model = poll(t, model)
	if model.dialog != dialogStart {
		t.Fatalf("start dialog = %v", model.dialog)
	}
	view := model.String()
	for _, want := range []string{"Start the current Queue run?", "P001 Кеш", "P001.1.2", "permissions: full", "without a sandbox", "apple watch", "needs a person"} {
		if !strings.Contains(view, want) {
			t.Fatalf("start dialog does not contain %q:\n%s", want, view)
		}
	}
	updated, _ = model.Update(keyMsg("esc"))
	model = updated.(Model)
	if len(first.answers) != 1 || first.answers[0].Confirmed {
		t.Fatalf("decline answers = %#v", first.answers)
	}
	if model.dialog != dialogNone || !strings.Contains(model.notice, "nothing ran") {
		t.Fatalf("decline dialog=%v notice=%q", model.dialog, model.notice)
	}
	first.finish(runner.StateFinished)
	model = poll(t, model)
	if model.runState != runner.StateFinished {
		t.Fatalf("declined run state = %s", model.runState)
	}

	// A second Start is refused while the host still owns an unfinished run,
	// and is allowed again only after it finished.
	third := newFakeController(runner.StateIdle)
	model.newController = func(string, string) Controller {
		model.newController = nil
		return third
	}
	updated, _ = model.Update(keyMsg("s"))
	model = updated.(Model)
	if third.started != 1 {
		t.Fatalf("Start after completion started %d controllers", third.started)
	}
	updated, _ = model.Update(keyMsg("s"))
	model = updated.(Model)
	if !strings.Contains(model.notice, "already active") || third.started != 1 {
		t.Fatalf("repeated Start: notice=%q started=%d", model.notice, third.started)
	}
}

func TestPauseResumeAndStopControls(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 100, 30)
	first := newFakeController(runner.StateIdle)
	second := newFakeController(runner.StateIdle)
	model = useControllers(t, model, first, second)

	updated, _ := model.Update(keyMsg("s"))
	model = updated.(Model)
	updated, _ = model.Update(keyMsg("p"))
	model = updated.(Model)
	if first.paused != 1 || model.runState != runner.StatePausing {
		t.Fatalf("pause: paused=%d state=%s", first.paused, model.runState)
	}
	if !strings.Contains(model.String(), "Pausing") {
		t.Fatal("Pausing is not shown before the checkpoint confirms it")
	}
	if first.checkpointed == false {
		t.Fatal("fake pause did not record a checkpoint")
	}
	first.finish(runner.StatePaused)
	model = poll(t, model)
	if model.runState != runner.StatePaused || !strings.Contains(model.notice, "Resume continues") {
		t.Fatalf("paused state=%s notice=%q", model.runState, model.notice)
	}
	updated, _ = model.Update(keyMsg("R"))
	model = updated.(Model)
	if second.resumed != 1 || model.runState != runner.StateRunning {
		t.Fatalf("resume: resumed=%d state=%s", second.resumed, model.runState)
	}
	updated, _ = model.Update(keyMsg("S"))
	model = updated.(Model)
	if second.stopped != 1 || model.runState != runner.StateStopping {
		t.Fatalf("stop after unit: stopped=%d state=%s", second.stopped, model.runState)
	}
}

func TestHumanBlockOffersContinueWithExistingUnblock(t *testing.T) {
	root := writeTestProject(t)
	planPath := filepath.Join(root, "docs", "plans", "P001-cache.md")
	text, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	blocked := strings.Replace(string(text), "### [!] P001.2.2 Заблокированный юнит\nDo:\n- ждать человека\n", "### [!] P001.2.2 Заблокированный юнит\nDo:\n- ждать человека\nHandoff:\n- Blocked (human): нужен вход в аккаунт\n", 1)
	if err := os.WriteFile(planPath, []byte(blocked), 0o644); err != nil {
		t.Fatal(err)
	}
	model := resize(t, loadedModel(t, root), 120, 40)
	first := newFakeController(runner.StateIdle)
	second := newFakeController(runner.StateIdle)
	model = useControllers(t, model, first, second)
	var calls []string
	model.lifecycle = func(root string, args ...string) cycle.Result {
		calls = append(calls, args...)
		return cycle.Result{Code: 0, Stdout: "commit: P001.2.2: unblock unit\n"}
	}

	updated, _ := model.Update(keyMsg("s"))
	model = updated.(Model)
	first.finish(runner.StateBlocked)
	model = poll(t, model)
	if model.dialog != dialogHuman {
		t.Fatalf("blocked dialog = %v (%s)", model.dialog, model.notice)
	}
	if !strings.Contains(model.String(), "нужен вход в аккаунт") || !strings.Contains(model.String(), "Continue") {
		t.Fatalf("block dialog lacks the reason or Continue:\n%s", model.String())
	}
	for _, part := range []string{"в", "о", "ш", "ё", "л"} {
		updated, _ = model.Update(keyMsg(part))
		model = updated.(Model)
	}
	updated, cmd := model.Update(keyMsg("enter"))
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("Enter on a human block returned no unblock command")
	}
	updated, _ = model.Update(cmd())
	model = updated.(Model)
	want := []string{"unblock", "P001.2.2", "--note=вошёл"}
	if strings.Join(calls, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("unblock call = %v, want %v", calls, want)
	}
	if second.started != 1 {
		t.Fatalf("Continue did not start the runner again: %d", second.started)
	}
}

func TestLifecycleActionsUseCLIAndShowLintRefusal(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	var calls [][]string
	model.lifecycle = func(root string, args ...string) cycle.Result {
		calls = append(calls, args)
		return cycle.Result{Code: 2, Stderr: "docs/plans/P002-draft.md:12: error: Read: missing file\n"}
	}
	model = selectRow(t, model, "plan:P002")
	if len(calls) != 0 {
		t.Fatal("selecting a draft plan ran a lifecycle command")
	}
	updated, cmd := model.Update(keyMsg("a"))
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("activate returned no command")
	}
	updated, _ = model.Update(cmd())
	model = updated.(Model)
	if len(calls) != 1 || strings.Join(calls[0], " ") != "activate P002" {
		t.Fatalf("lifecycle calls = %v", calls)
	}
	if !strings.Contains(model.notice, "activate P002 failed") || !strings.Contains(model.notice, "Read: missing file") {
		t.Fatalf("lint refusal notice = %q", model.notice)
	}
	if model.snapshot.Queue[1].Status != "draft" {
		t.Fatalf("refused activation changed the plan status to %q", model.snapshot.Queue[1].Status)
	}
}

func TestCloseNoticeNamesUnwrittenArchiveText(t *testing.T) {
	root := writeTestProject(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := "---\nid: P001\ntitle: cache\nclosed: 2026-11-01\n---\n## Outcome           <!-- 2–3 строки: что теперь есть -->\n\n## Decisions\n\n## Gotchas\n\n## Follow-ups\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "archive", "P001-cache.md"), []byte(archive), 0o644); err != nil {
		t.Fatal(err)
	}
	model := resize(t, loadedModel(t, root), 120, 40)
	model.lifecycle = func(string, ...string) cycle.Result {
		return cycle.Result{Code: 0, Stdout: "docs/archive/P001-cache.md\n"}
	}
	model = selectRow(t, model, "plan:P001")
	updated, cmd := model.Update(keyMsg("C"))
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("close returned no command")
	}
	updated, cmd = model.Update(cmd())
	model = updated.(Model)
	want := "needs your Outcome text"
	if !strings.Contains(model.notice, want) || !strings.Contains(model.notice, "docs/archive/P001-cache.md") {
		t.Fatalf("close notice = %q", model.notice)
	}
	model = applyLoad(t, model, cmd)
	if !strings.Contains(model.notice, want) || !strings.Contains(model.notice, "docs/archive/P001-cache.md") {
		t.Fatalf("reload replaced the close notice with %q", model.notice)
	}
	if !strings.Contains(model.statusLine(), "docs/archive/P001-cache.md") {
		t.Fatalf("status line lost the close notice: %q", model.statusLine())
	}
}

// applyLoad runs one command, applies every snapshotMsg it carries, and
// insists the command really scheduled a plan reload: a test that silently
// lost the loadCmd would pass without exercising the reload at all.
func applyLoad(t *testing.T, model Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command returned; the plan reload would never run")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		batch = tea.BatchMsg{func() tea.Msg { return msg }}
	}
	applied := false
	for _, sub := range batch {
		if sub == nil {
			continue
		}
		snapshot, ok := sub().(snapshotMsg)
		if !ok {
			continue
		}
		updated, _ := model.Update(snapshot)
		model = updated.(Model)
		applied = true
	}
	if !applied {
		t.Fatalf("command carried no snapshotMsg, got %T", msg)
	}
	return model
}

func TestReloadPlaceholderIsCleared(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	updated, cmd := model.Update(keyMsg("r"))
	model = updated.(Model)
	model = applyLoad(t, model, cmd)
	if model.notice != "" {
		t.Fatalf("completed reload kept the placeholder: %q", model.notice)
	}
	if strings.Contains(model.statusLine(), reloadingText) {
		t.Fatalf("status line still says reloading: %q", model.statusLine())
	}
	// A new action replaces the previous outcome instead of stacking on it.
	updated, _ = model.Update(keyMsg("p"))
	model = updated.(Model)
	if model.notice != "Pause needs a running run" {
		t.Fatalf("new action did not replace the notice: %q", model.notice)
	}
}

func TestActivateNoticeSurvivesReload(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	model.lifecycle = func(string, ...string) cycle.Result {
		return cycle.Result{Code: 0, Stdout: "activated P002\n"}
	}
	model = selectRow(t, model, "plan:P002")
	updated, cmd := model.Update(keyMsg("a"))
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("activate returned no command")
	}
	updated, cmd = model.Update(cmd())
	model = updated.(Model)
	if !strings.Contains(model.notice, "activate P002: activated P002") {
		t.Fatalf("activate notice = %q", model.notice)
	}
	model = applyLoad(t, model, cmd)
	if !strings.Contains(model.notice, "activate P002: activated P002") {
		t.Fatalf("reload replaced the activate notice with %q", model.notice)
	}
	if !strings.Contains(model.statusLine(), "activate P002: activated P002") {
		t.Fatalf("status line lost the activate notice: %q", model.statusLine())
	}
}

func TestRunFailureNoticeSurvivesReload(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	controller := newFakeController(runner.StateIdle)
	model = useControllers(t, model, controller)
	updated, _ := model.Update(keyMsg("s"))
	model = updated.(Model)
	controller.err = errors.New("agent exited 3")
	controller.finish(runner.StateFailed)
	updated, cmd := model.Update(hostPollMsg{})
	model = updated.(Model)
	want := "run failed: agent exited 3"
	if !strings.Contains(model.notice, want) {
		t.Fatalf("failure notice = %q", model.notice)
	}
	model = applyLoad(t, model, cmd)
	if !strings.Contains(model.notice, want) {
		t.Fatalf("reload replaced the failure notice with %q", model.notice)
	}
	if !strings.Contains(model.statusLine(), want) {
		t.Fatalf("status line lost the failure notice: %q", model.statusLine())
	}
}

func TestPausedNoticeSurvivesReload(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	controller := newFakeController(runner.StateIdle)
	model = useControllers(t, model, controller)
	updated, _ := model.Update(keyMsg("s"))
	model = updated.(Model)
	updated, _ = model.Update(keyMsg("p"))
	model = updated.(Model)
	controller.finish(runner.StatePaused)
	updated, cmd := model.Update(hostPollMsg{})
	model = updated.(Model)
	want := "paused; Resume continues"
	if !strings.Contains(model.notice, want) {
		t.Fatalf("pause notice = %q", model.notice)
	}
	model = applyLoad(t, model, cmd)
	if !strings.Contains(model.notice, want) {
		t.Fatalf("reload replaced the pause notice with %q", model.notice)
	}
	if !strings.Contains(model.statusLine(), want) {
		t.Fatalf("status line lost the pause notice: %q", model.statusLine())
	}
}

func TestPausingNoticeSurvivesUnitEndReload(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	controller := newFakeController(runner.StateIdle)
	model = useControllers(t, model, controller)
	updated, _ := model.Update(keyMsg("s"))
	model = updated.(Model)
	updated, _ = model.Update(keyMsg("p"))
	model = updated.(Model)
	want := "pausing; the current agent call stops"
	if !strings.Contains(model.notice, want) {
		t.Fatalf("pause notice = %q", model.notice)
	}
	updated, cmd := model.Update(ActivityMsg{Event: runner.Event{Type: "unit_end", Unit: "P001.1.1", Fields: map[string]any{"status": "done"}}})
	model = updated.(Model)
	model = applyLoad(t, model, cmd)
	if !strings.Contains(model.notice, want) {
		t.Fatalf("unit_end reload replaced the pause notice with %q", model.notice)
	}
	if !strings.Contains(model.statusLine(), want) {
		t.Fatalf("status line lost the pause notice: %q", model.statusLine())
	}
}

func TestSignalStopPausesOnceAndWaitsForTheCheckpoint(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 100, 30)
	controller := newFakeController(runner.StateIdle)
	controller.pauseFinishes = true
	model = useControllers(t, model, controller)
	updated, _ := model.Update(keyMsg("s"))
	model = updated.(Model)
	if err := model.stopAfterSignal(); err != nil {
		t.Fatalf("signal stop = %v", err)
	}
	if controller.paused != 1 || controller.closed != 1 {
		t.Fatalf("paused=%d closed=%d, want one pause and one close", controller.paused, controller.closed)
	}
}

func TestSignalStopTimesOutAndClosesTheHost(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 100, 30)
	controller := newFakeController(runner.StateIdle)
	model = useControllers(t, model, controller)
	updated, _ := model.Update(keyMsg("s"))
	model = updated.(Model)
	previous := stopWaitTimeout
	stopWaitTimeout = 50 * time.Millisecond
	t.Cleanup(func() { stopWaitTimeout = previous })
	err := model.stopAfterSignal()
	if err == nil || !strings.Contains(err.Error(), "did not stop within") {
		t.Fatalf("timeout error = %v", err)
	}
	if controller.closed != 1 {
		t.Fatalf("controller closed %d times after the timeout", controller.closed)
	}
}

func TestSignalStopRefusesPausedWithoutCheckpoint(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 100, 30)
	controller := newFakeController(runner.StateIdle)
	controller.pauseFinishes = true
	controller.pauseNoCheckpoint = true
	model = useControllers(t, model, controller)
	updated, _ := model.Update(keyMsg("s"))
	model = updated.(Model)
	err := model.stopAfterSignal()
	if err == nil || !strings.Contains(err.Error(), "Paused without") {
		t.Fatalf("unwritten checkpoint error = %v", err)
	}
}

func TestSignalStopCancelsAPendingStart(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 100, 30)
	controller := newFakeController(runner.StateStarting)
	model.controller = controller
	model.runState = runner.StateStarting
	if err := model.stopAfterSignal(); err != nil {
		t.Fatalf("signal stop during start = %v", err)
	}
	if controller.canceled != 1 || controller.closed != 1 {
		t.Fatalf("canceled=%d closed=%d, want one cancel and one close", controller.canceled, controller.closed)
	}
	if controller.paused != 0 {
		t.Fatalf("a pending start was paused %d times", controller.paused)
	}
}

func TestQuitWithLiveRunPausesAndWaits(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 100, 30)
	controller := newFakeController(runner.StateIdle)
	model = useControllers(t, model, controller)
	updated, _ := model.Update(keyMsg("s"))
	model = updated.(Model)

	updated, _ = model.Update(keyMsg("q"))
	model = updated.(Model)
	if model.dialog != dialogExit {
		t.Fatalf("quit with a live run opened dialog %v", model.dialog)
	}
	updated, _ = model.Update(keyMsg("enter"))
	model = updated.(Model)
	if controller.paused != 1 || !controller.checkpointed || model.runState != runner.StatePausing {
		t.Fatalf("exit did not pause: paused=%d checkpoint=%v state=%s", controller.paused, controller.checkpointed, model.runState)
	}
	controller.finish(runner.StatePaused)
	updated, cmd := model.Update(hostPollMsg{})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("a confirmed exit did not return the quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("exit command = %T, want tea.QuitMsg", cmd())
	}
	if controller.closed != 1 {
		t.Fatalf("controller closed %d times after exit", controller.closed)
	}
}

func TestForeignRunnerLockAllowsViewingButRefusesControl(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 100, 30)
	model.lockCheck = func(string) (int, bool, error) { return 4242, true, nil }
	model.refreshLock()
	model.newController = func(string, string) Controller {
		t.Fatal("a foreign runner lock allowed a new controller")
		return nil
	}
	if !strings.Contains(model.String(), "P001.1.2") {
		t.Fatal("a foreign runner lock hid the plans")
	}
	updated, _ := model.Update(keyMsg("s"))
	model = updated.(Model)
	if !strings.Contains(model.notice, "another runner is active") {
		t.Fatalf("start notice = %q", model.notice)
	}
	model = selectRow(t, model, "plan:P002")
	updated, cmd := model.Update(keyMsg("a"))
	model = updated.(Model)
	if cmd != nil || !strings.Contains(model.notice, "lifecycle changes are refused") {
		t.Fatalf("activate under a foreign lock: cmd=%v notice=%q", cmd, model.notice)
	}
}

// TestControlChecksTheLockAtActionTime changes the lock owner after the load:
// start and lifecycle refuse without touching their seams, and the same keys
// pass once the lock is gone.
func TestControlChecksTheLockAtActionTime(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	model.lockCheck = func(string) (int, bool, error) { return 4242, true, nil }
	created := 0
	model.newController = func(string, string) Controller {
		created++
		return newFakeController(runner.StateIdle)
	}
	activated := ""
	model.lifecycle = func(_ string, args ...string) cycle.Result {
		if len(args) > 0 {
			activated = args[0]
		}
		return cycle.Result{Code: 0, Stdout: "activated"}
	}

	updated, _ := model.Update(keyMsg("s"))
	model = updated.(Model)
	if !strings.Contains(model.notice, "another runner is active") || !strings.Contains(model.notice, "4242") {
		t.Fatalf("start under a lock that appeared after load: notice = %q", model.notice)
	}
	model = selectRow(t, model, "plan:P002")
	updated, cmd := model.Update(keyMsg("a"))
	model = updated.(Model)
	if cmd != nil || !strings.Contains(model.notice, "lifecycle changes are refused") {
		t.Fatalf("activate under a late lock: cmd=%v notice=%q", cmd, model.notice)
	}
	if created != 0 || activated != "" {
		t.Fatalf("a late lock called a seam: created=%d activated=%q", created, activated)
	}
	if !strings.Contains(model.String(), "P002") {
		t.Fatal("a late lock hid the plans from the tree")
	}

	model.lockCheck = func(string) (int, bool, error) { return 0, false, nil }
	model = selectRow(t, model, "plan:P002")
	updated, cmd = model.Update(keyMsg("a"))
	model = updated.(Model)
	if cmd == nil {
		t.Fatalf("activate after the lock was removed: notice = %q", model.notice)
	}
	if msg, ok := cmd().(lifecycleDoneMsg); !ok || msg.action != "activate" {
		t.Fatalf("activate after the lock was removed produced %#v", cmd())
	}
	if activated != "activate" {
		t.Fatalf("lifecycle seam saw %q, want activate", activated)
	}

	updated, _ = model.Update(keyMsg("s"))
	model = updated.(Model)
	if created != 1 {
		t.Fatalf("start after the lock was removed created %d controllers, want 1 (notice %q)", created, model.notice)
	}
}

// TestUnreadableLockRefusesControl treats a failing lock check as unknown:
// the workspace never calls the checkout free, but keeps the plans readable.
func TestUnreadableLockRefusesControl(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	model.lockCheck = func(string) (int, bool, error) { return 0, false, os.ErrPermission }
	model.newController = func(string, string) Controller {
		t.Fatal("an unreadable lock allowed a new controller")
		return nil
	}
	model.lifecycle = func(string, ...string) cycle.Result {
		t.Fatal("an unreadable lock allowed a lifecycle command")
		return cycle.Result{}
	}

	updated, _ := model.Update(keyMsg("s"))
	model = updated.(Model)
	if !strings.Contains(model.notice, "cannot read the checkout lock") || !strings.Contains(model.notice, "permission denied") {
		t.Fatalf("start under an unreadable lock: notice = %q", model.notice)
	}
	model = selectRow(t, model, "plan:P002")
	updated, cmd := model.Update(keyMsg("a"))
	model = updated.(Model)
	if cmd != nil || !strings.Contains(model.notice, "cannot read the checkout lock") {
		t.Fatalf("activate under an unreadable lock: cmd=%v notice=%q", cmd, model.notice)
	}
	if !strings.Contains(model.String(), "lock:unknown") {
		t.Fatalf("status line does not report the unknown lock:\n%s", model.String())
	}
	if !strings.Contains(model.String(), "P002") {
		t.Fatal("an unreadable lock hid the plans from the tree")
	}
}

// TestFinishedRunRefreshesTheLock keeps the status line and the guard in step
// after the workspace's own run stopped and released its lock.
func TestFinishedRunRefreshesTheLock(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	held := false
	model.lockCheck = func(string) (int, bool, error) {
		if held {
			return 4242, true, nil
		}
		return 0, false, nil
	}
	controller := newFakeController(runner.StateIdle)
	model = useControllers(t, model, controller)
	updated, _ := model.Update(keyMsg("s"))
	model = updated.(Model)
	if model.lockHeld {
		t.Fatal("the workspace's own start reported a foreign lock")
	}
	held = true
	controller.finish(runner.StateFinished)
	updated, _ = model.Update(hostPollMsg{})
	model = updated.(Model)
	if !model.lockHeld || model.lockPID != 4242 {
		t.Fatalf("after the run finished: held=%v pid=%d", model.lockHeld, model.lockPID)
	}
	model = selectRow(t, model, "plan:P002")
	updated, cmd := model.Update(keyMsg("a"))
	model = updated.(Model)
	if cmd != nil || !strings.Contains(model.notice, "lifecycle changes are refused") {
		t.Fatalf("activate after a run exposed a foreign lock: cmd=%v notice=%q", cmd, model.notice)
	}
}
