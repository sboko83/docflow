package tui

import (
	"strings"
	"testing"

	"bsv-docflow/internal/runner"
)

// TestRunLimitsReachTheRunnerArgs keeps the command-line limits on the only
// path that builds the host: WithRunLimits through the workspace options.
func TestRunLimitsReachTheRunnerArgs(t *testing.T) {
	root := writeTestProject(t)

	withLimits := options{}
	WithRunLimits(1, 0)(&withLimits)
	model := applyOptions(New(root), withLimits)
	host, ok := model.newHost(root, "").(*Host)
	if !ok {
		t.Fatal("the default factory did not build a runner host")
	}
	if host.args.Attempts != 1 || host.args.Replans != 0 {
		t.Fatalf("runner args = %d attempts/%d replans, want 1/0", host.args.Attempts, host.args.Replans)
	}

	model = applyOptions(New(root), options{})
	host, ok = model.newHost(root, "").(*Host)
	if !ok {
		t.Fatal("the default factory did not build a runner host")
	}
	if host.args.Attempts != 3 || host.args.Replans != 3 {
		t.Fatalf("default runner args = %d attempts/%d replans, want 3/3", host.args.Attempts, host.args.Replans)
	}
}

// TestStartDialogShowsRunLimits keeps the effective limits visible before the
// run starts, next to the assignments.
func TestStartDialogShowsRunLimits(t *testing.T) {
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	first := newFakeController(runner.StateIdle)
	first.prompt("confirm_start", map[string]any{"preview": runner.StartReview{
		Plan: "P001", Title: "Кеш", Unit: "P001.1.2", Kind: "unit",
		Attempts: 1, Replans: 0,
	}})
	model = useControllers(t, model, first)
	model = send(t, model, keyMsg("s"))
	model = poll(t, model)
	if model.dialog != dialogStart {
		t.Fatalf("start dialog = %v", model.dialog)
	}
	if view := model.String(); !strings.Contains(view, "attempts 1 · auto-replan off") {
		t.Fatalf("start dialog does not show the run limits:\n%s", view)
	}
}
