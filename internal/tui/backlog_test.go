package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bsv-docflow/internal/roster"
	"bsv-docflow/internal/runner"
	tea "charm.land/bubbletea/v2"
)

func TestBacklogKeysAndMultilineEditor(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 32, 10)
	model = send(t, model, keyMsg("b"))
	if model.dialog != dialogBacklog || !strings.Contains(Sanitize(model.String()), "later work") {
		t.Fatal(flatScreen(model))
	}
	model = send(t, model, keyMsg("+"))
	model = send(t, model, tea.PasteMsg{Content: "первая\r\nвторая"})
	model = send(t, model, keyMsg("home"))
	model = send(t, model, keyMsg("right"))
	model = send(t, model, keyMsg("down"))
	model = send(t, model, keyMsg("backspace"))
	model = send(t, model, keyMsg("X"))
	model = send(t, model, keyMsg("end"))
	model = send(t, model, keyMsg("enter"))
	model = send(t, model, keyMsg("tail"))
	updated, cmd := model.Update(keyMsg("ctrl+s"))
	model = applyLoad(t, updated.(Model), cmd)
	if model.snapshot.Backlog != 2 {
		t.Fatal(model.snapshot)
	}
	path := filepath.Join(root, "docs/docflow.md")
	data, _ := os.ReadFile(path)
	want := strings.TrimSuffix(testDocflow, "\n") + "\n- первая\n  Xторая\n  tail\n"
	if string(data) != want {
		t.Fatalf("got %q, want %q", data, want)
	}
	model = send(t, model, keyMsg("down"))
	model = send(t, model, keyMsg("enter"))
	if model.dialogInput != "первая\nXторая\ntail" {
		t.Fatal(model.dialogInput)
	}
	model = send(t, model, keyMsg("home"))
	model = send(t, model, keyMsg("delete"))
	updated, cmd = model.Update(keyMsg("ctrl+s"))
	model = applyLoad(t, updated.(Model), cmd)
	model = send(t, model, keyMsg("D"))
	model = send(t, model, keyMsg("esc"))
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), "- ервая") {
		t.Fatal(string(data))
	}
	model = send(t, model, keyMsg("D"))
	updated, cmd = model.Update(keyMsg("enter"))
	model = applyLoad(t, updated.(Model), cmd)
	data, _ = os.ReadFile(path)
	if string(data) != testDocflow || model.snapshot.Backlog != 1 {
		t.Fatalf("delete did not restore fixture: %q", data)
	}
	model = send(t, model, keyMsg("esc"))
	if model.dialog != dialogNone {
		t.Fatal("Esc did not return to project")
	}
}

func TestBacklogSaveRechecksLockRunAndFile(t *testing.T) {
	for _, mode := range []string{"lock", "run", "stale", "delete-lock"} {
		t.Run(mode, func(t *testing.T) {
			root := writeTestProject(t)
			model := loadedModel(t, root).openBacklog()
			model = model.editBacklog(0)
			model.dialogInput = "replacement"
			path := filepath.Join(root, "docs/docflow.md")
			before, _ := os.ReadFile(path)
			switch mode {
			case "lock", "delete-lock":
				model.lockCheck = func(string) (int, bool, error) { return 123, true, nil }
			case "run":
				model.controller = &fakeController{}
				model.runState = runner.StateRunning
			case "stale":
				before = append(before, []byte("\nexternal\n")...)
				if err := os.WriteFile(path, before, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			model, cmd := model.saveBacklog(mode == "delete-lock")
			after, _ := os.ReadFile(path)
			if cmd != nil || string(before) != string(after) || model.notice == "" {
				t.Fatalf("refused write: %q, %q", after, model.notice)
			}
		})
	}
}

func TestBacklogPlanningRouteAndRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		roles []roster.RoleBrief
		want  string
	}{
		{"heavy", []roster.RoleBrief{{Role: "executor", Routes: []roster.Candidate{{Agent: "pi", Model: "fallback"}}}, {Role: "heavy", Routes: []roster.Candidate{{Agent: "codex", Model: "primary"}, {Agent: "pi", Model: "second"}}}}, "primary"},
		{"executor", []roster.RoleBrief{{Role: "heavy"}, {Role: "executor", Routes: []roster.Candidate{{Agent: "pi", Model: "fallback"}}}}, "fallback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &roster.Workspace{Roles: tc.roles}
			route, err := w.PlanningRoute()
			if err != nil || route.Model != tc.want {
				t.Fatalf("route=%+v err=%v", route, err)
			}
		})
	}
	for _, mode := range []string{"unsupported", "missing-cli", "missing-roster", "no-route", "load-error"} {
		t.Run(mode, func(t *testing.T) {
			root := writeTestProject(t)
			model := resize(t, loadedModel(t, root), 32, 10).openBacklog()
			agent := "custom"
			if mode == "missing-cli" {
				agent = "pi"
				t.Setenv("PATH", t.TempDir())
			}
			model.rosterLoad = func(string, string) (*roster.Workspace, error) {
				if mode == "load-error" {
					return nil, errors.New("broken roster")
				}
				if mode == "missing-roster" {
					return &roster.Workspace{Missing: true}, nil
				}
				if mode == "no-route" {
					return &roster.Workspace{}, nil
				}
				return &roster.Workspace{Roles: []roster.RoleBrief{{Role: "heavy", Routes: []roster.Candidate{{Agent: agent, Model: "model"}}}}}, nil
			}
			model, cmd := model.planBacklog()
			if cmd != nil || model.dialog != dialogPlanningPrompt || !strings.Contains(strings.Join(model.dialogLines, "\n"), "bsv-docflow-plan") || !strings.Contains(model.planningPrompt, "later work") {
				t.Fatalf("refusal=%+v", model.dialogLines)
			}
			updated, cmd := model.Update(keyMsg("y"))
			model = updated.(Model)
			if cmd == nil {
				t.Fatal("no clipboard command")
			}
			if got := fmt.Sprint(cmd()); got != model.planningPrompt {
				t.Fatalf("clipboard = %q", got)
			}
			// The clipboard command contains the exact original prompt, not its wrapped preview.
			if !strings.Contains(strings.Join(model.dialogLines, "\n"), model.planningPrompt) {
				t.Fatal(model.dialogLines)
			}
			model = send(t, model, keyMsg("esc"))
			if model.dialog != dialogBacklog {
				t.Fatal(model.dialog)
			}
		})
	}
}

func TestBacklogMinimumSizeWithNoticeAndEmptyQueue(t *testing.T) {
	root := writeTestProject(t)
	path := filepath.Join(root, "docs/docflow.md")
	writeRosterTestFile(t, path, "---\ndocflow: 3\n---\n## Queue\n\n## Backlog\n")
	model := resize(t, loadedModel(t, root), 32, 10)
	model.notice = "Backlog saved; commit project files before starting a run"
	model = model.openBacklog()
	if !strings.Contains(flatScreen(model), "Backlogisempty") {
		t.Fatal(model.String())
	}
	model = model.editBacklog(-1).insertBacklogText(strings.Repeat("длинная строка\n", 20) + "end")
	view := flatScreen(model)
	if !strings.Contains(view, "end▏") || !strings.Contains(view, "Ctrl+S") || !strings.Contains(view, "Esc") {
		t.Fatal(model.String())
	}
	updated, cmd := model.Update(keyMsg("ctrl+s"))
	model = applyLoad(t, updated.(Model), cmd)
	if !strings.Contains(flatScreen(model), ">длиннаястрока") {
		t.Fatal(model.String())
	}
}

func TestBacklogPlanningRechecksLockAfterRosterLoad(t *testing.T) {
	root := writeTestProject(t)
	model := loadedModel(t, root).openBacklog()
	locked := false
	model.lockCheck = func(string) (int, bool, error) { return 123, locked, nil }
	model.rosterLoad = func(string, string) (*roster.Workspace, error) {
		locked = true
		return &roster.Workspace{Roles: []roster.RoleBrief{{Role: "heavy", Routes: []roster.Candidate{{Agent: "pi", Model: "model"}}}}}, nil
	}
	// A test binary is sufficient for LookPath: the lock must refuse it
	// before the interactive process can be started.
	bin := t.TempDir()
	name := "pi"
	if os.PathSeparator == '\\' {
		name += ".exe"
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, name), content, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	model, cmd := model.planBacklog()
	if cmd != nil || !strings.Contains(model.notice, "refused") || model.dialog != dialogBacklog {
		t.Fatalf("late lock: cmd=%v notice=%q dialog=%v", cmd, model.notice, model.dialog)
	}
}
