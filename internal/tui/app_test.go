package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"bsv-docflow/internal/format"
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
