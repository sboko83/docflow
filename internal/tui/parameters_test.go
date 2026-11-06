package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func parameterTestProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "plans"), 0o755); err != nil {
		t.Fatal(err)
	}
	docflow := "---\ndocflow: 3\nread_budget: 40000\n---\n## Queue\n- P001\n\n## Frozen\n\n## Backlog\n"
	plan := "---\nid: P001\ntitle: Parameters\ntype: feature\nstatus: active\ncreated: 2026-11-06\n---\n## Goal\n\nTest parameters.\n## Context\n\nTest.\n## Constraints\n\nTest.\n## Traps\n\nTest.\n## Decisions\n\nTest.\n## Phase 1: Work\n\n### [ ] P001.1.1 Test settings\nRead:\n- docs/docflow.md\nDo:\n- test\nVerify: `true`\nCommit: P001.1.1: test\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "docflow.md"), []byte(docflow), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "plans", "P001-parameters.md"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestParameterMetadataWritesAndLints(t *testing.T) {
	root := parameterTestProject(t)
	model := loadedModel(t, root)
	plan := model.snapshot.Queue[0]
	rows, err := CollectParameters(root, plan, DefaultRunLimits())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"read_budget": "50000", "start_read": "docs/docflow.md", "start_budget": "5000",
		"permissions": "auto", "timeout": "120", "gate": "go test ./...",
	}
	for _, row := range rows {
		value, ok := want[row.Key]
		if !ok {
			continue
		}
		if err := SaveParameter(root, row, value); err != nil {
			t.Fatalf("save %s: %v", row.Key, err)
		}
		delete(want, row.Key)
	}
	if len(want) != 0 {
		t.Fatalf("parameters not found: %v", want)
	}
	if _, err := CollectParameters(root, plan, DefaultRunLimits()); err != nil {
		t.Fatalf("reload parameters: %v", err)
	}
}

func TestInvalidTimeoutRestoresOriginalFile(t *testing.T) {
	root := parameterTestProject(t)
	planPath := filepath.Join(root, "docs", "plans", "P001-parameters.md")
	original, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	parameter := Parameter{Scope: "Plan P001", Key: "timeout", Path: planPath, Disk: true}
	if err := SaveParameter(root, parameter, "12"); err == nil || !strings.Contains(err.Error(), "lint rejected") {
		t.Fatalf("SaveParameter error = %v, want lint rejection", err)
	}
	got, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatal("invalid timeout was not rolled back byte for byte")
	}
}

func TestEditedSessionLimitsReachNextRunnerArgs(t *testing.T) {
	root := parameterTestProject(t)
	model := New(root)
	model.runLimits = RunLimits{Attempts: 1, Replans: 0}
	host, ok := model.newHost(root, "").(*Host)
	if !ok {
		t.Fatal("the default factory did not build a runner host")
	}
	if host.args.Attempts != 1 || host.args.Replans != 0 {
		t.Fatalf("runner args = %d/%d, want edited session limits 1/0", host.args.Attempts, host.args.Replans)
	}
}

func TestParametersScreenEditsSessionForNextStart(t *testing.T) {
	root := parameterTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	model.cursor = 1
	model = send(t, model, keyMsg("o"))
	if model.dialog != dialogParameters {
		t.Fatalf("dialog = %v, want parameters", model.dialog)
	}
	for range 6 {
		model = send(t, model, keyMsg("down"))
	}
	if model.parameterCursor >= len(model.parameters) || model.parameters[model.parameterCursor].Key != "attempts" {
		t.Fatalf("parameter selection = %d/%d", model.parameterCursor, len(model.parameters))
	}
	model = resize(t, model, 32, 10)
	if view := model.String(); !strings.Contains(view, "attempts") {
		t.Fatalf("selected parameter is not visible at 32x10:\n%s", view)
	}
	model = send(t, model, keyMsg("enter"))
	model = send(t, model, tea.KeyPressMsg{Code: tea.KeyBackspace})
	model = send(t, model, tea.KeyPressMsg{Text: "1"})
	model = send(t, model, keyMsg("enter"))
	if model.runLimits.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", model.runLimits.Attempts)
	}
	host, ok := model.newHost(root, "").(*Host)
	if !ok {
		t.Fatal("the default factory did not build a runner host")
	}
	if host.args.Attempts != 1 || host.args.Replans != 3 {
		t.Fatalf("runner args = %d/%d, want 1/3", host.args.Attempts, host.args.Replans)
	}
}
