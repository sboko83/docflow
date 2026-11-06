package tui

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bsv-docflow/internal/roster"
	"bsv-docflow/internal/runner"
)

// The apply tests read a project with one plan whose header is stale and a
// roster whose presets reorder or switch the accesses off. The plan's routes
// come from the same resolver the CLI uses, so the tests never spell out a
// route the roster would not write.

const applyRosterPolicy = `version = 2

[access.zai]
agents = ["pi"]
kind   = "subscription"
budget = "normal"

[access.codex]
agents  = ["codex"]
kind    = "subscription"
budget  = "normal"
trusted = true
expires = "2099-06-30"

[access.claude]
agents  = ["claude"]
kind    = "subscription"
budget  = "off"
trusted = true

[model."zai/glm-5.3"]
class = "standard"

[model."zai/glm-5.3-flash"]
class = "cheap"

[model."codex/gpt-6.1-sol"]
class = "strong"

[model."codex/gpt-6-luna"]
class = "standard"

[model."claude/claude-opus-5-5"]
class = "strong"

[role]
cheap      = "cheap"
executor   = "standard"
heavy      = "strong"
review     = "strong"
acceptance = "trusted"
replan     = "trusted"
`

// applyVariants reorders the accesses for the card warning test and switches
// every route off for the refusal test.
const applyVariants = `version = 2

[preset.codex-first]
order = ["codex", "zai", "claude"]

[preset.quiet]
off = ["zai", "codex", "claude"]
`

const applyDocflow = `---
docflow: 3
---
## Queue
- P700

## Frozen

## Backlog
`

// applyPlanText is the plan of the fixture: the executor header is stale, so
// every apply rewrites at least that key.
func applyPlanText() string {
	return `---
id: P700
title: Apply roster
type: feature
status: active
created: 2026-11-01
executor: pi | codex/gpt-6-luna | high
---
## Goal

Apply fixture.

## Context

## Constraints

## Traps

## Decisions

## Phase 1: One

### [ ] P700.1.1 First unit
Read:
- a.go
Do:
- work
Verify: ` + "`true`" + `
Commit: P700.1.1: first

### [ ] P700.1.2 Cheap unit
Read:
- a.go
Do:
- work
Verify: ` + "`true`" + `
Role: cheap
Commit: P700.1.2: cheap
`
}

// applyFixture writes the project with a git directory — apply reads the
// checkout lock — the roster under a private config and the catalog cache.
func applyFixture(t *testing.T) (root, rosterPath string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root = t.TempDir()
	writeRosterTestFile(t, filepath.Join(root, "docs", "docflow.md"), applyDocflow)
	writeRosterTestFile(t, filepath.Join(root, "docs", "plans", "P700-apply.md"), applyPlanText())
	writeRosterTestFile(t, filepath.Join(root, "a.go"), "package a\n")
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "docflow test"},
		{"config", "user.email", "test@example.invalid"},
		{"add", "-A"},
		{"commit", "-q", "-m", "initial"},
	} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	rosterPath = filepath.Join(config, "docflow", "roster.toml")
	writeRosterTestFile(t, rosterPath, applyRosterPolicy)
	writeRosterTestFile(t, roster.VariantsPath(rosterPath), applyVariants)
	writeRosterTestCatalog(t)
	return root, rosterPath
}

func applyPlanFile(root string) string {
	return filepath.Join(root, "docs", "plans", "P700-apply.md")
}

// applyModel loads the fixture and selects the plan row on the Проект screen
// with the roster already read.
func applyModel(t *testing.T, root, rosterPath string) Model {
	t.Helper()
	model := rosterScreenModel(t, root, rosterPath)
	return selectRow(t, model, "plan:P700")
}

// TestApplyDialogShowsTheDiffAndWritesTheHeader: A explains the header
// against the roster, Esc keeps the file, Enter writes the header, reports
// the commit reminder and reloads the workspace; a second A finds nothing
// to write.
func TestApplyDialogShowsTheDiffAndWritesTheHeader(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := applyFixture(t)
	model := applyModel(t, root, rosterPath)
	before := readRosterTestFile(t, applyPlanFile(root))

	model = send(t, model, keyMsg("A"))
	if model.dialog != dialogApply {
		t.Fatalf("A opened dialog %v, want the apply dialog", model.dialog)
	}
	body := strings.Join(model.dialogLines, "\n")
	for _, want := range []string{"P700", "вариант: нет активного", "executor: pi | codex/gpt-6-luna | high → ", "replan: - → ", "cheap: - → ", "⚠ no fallback for replan"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the dialog lost %q:\n%s", want, body)
		}
	}
	// The difference is shown before anything is written.
	if got := readRosterTestFile(t, applyPlanFile(root)); got != before {
		t.Fatalf("opening the dialog rewrote the plan:\n%s", got)
	}
	// The dialog answers the smallest supported terminal: the keys that write
	// and cancel stay on screen while the body scrolls.
	tiny := resize(t, model, 32, 10)
	assertScreenFits(t, tiny.String(), 32, 10)
	if rendered := tiny.String(); !strings.Contains(rendered, "Enter — записать") {
		t.Fatalf("32x10 lost the dialog keys:\n%s", rendered)
	}

	model = send(t, model, keyMsg("esc"))
	if model.dialog != dialogNone || model.applyPlan != "" {
		t.Fatalf("Esc kept the dialog: %v %q", model.dialog, model.applyPlan)
	}
	if !strings.Contains(model.notice, "шапка не изменена") {
		t.Fatalf("Esc notice = %q", model.notice)
	}
	if got := readRosterTestFile(t, applyPlanFile(root)); got != before {
		t.Fatalf("a cancelled apply rewrote the plan:\n%s", got)
	}

	model = send(t, model, keyMsg("A"))
	updated, cmd := model.Update(keyMsg("enter"))
	model = updated.(Model)
	if model.dialog != dialogNone {
		t.Fatalf("Enter kept the dialog: %q", model.notice)
	}
	if model.notice != "commit: P700: apply roster" {
		t.Fatalf("notice = %q, want the commit reminder", model.notice)
	}
	written := readRosterTestFile(t, applyPlanFile(root))
	for _, want := range []string{"executor: pi | zai/glm-5.3", "cheap: pi | zai/glm-5.3-flash", "replan: codex | gpt-6.1-sol"} {
		if !strings.Contains(written, want) {
			t.Fatalf("the written header lost %q:\n%s", want, written)
		}
	}
	if strings.Contains(written, "codex/gpt-6-luna") {
		t.Fatalf("the stale executor stayed in the header:\n%s", written)
	}
	model = applyLoad(t, model, cmd)

	model = send(t, model, keyMsg("A"))
	if body := strings.Join(model.dialogLines, "\n"); !strings.Contains(body, "изменений нет") {
		t.Fatalf("the second A does not report an empty difference:\n%s", body)
	}
	model = send(t, model, keyMsg("enter"))
	if model.dialog != dialogApply {
		t.Fatalf("Enter on an empty difference closed the dialog")
	}
	if body := strings.Join(model.dialogLines, "\n"); !strings.Contains(body, "изменений нет") {
		t.Fatalf("the refused empty apply lost its reason:\n%s", body)
	}
	if got := readRosterTestFile(t, applyPlanFile(root)); got != written {
		t.Fatalf("an empty apply rewrote the plan:\n%s", got)
	}
}

// TestApplyDialogRefusesAnOpenUnitWithoutChain: with every route off the
// executor chain is empty, and apply refuses the open unit before writing.
func TestApplyDialogRefusesAnOpenUnitWithoutChain(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := applyFixture(t)
	model := applyModel(t, root, rosterPath)
	before := readRosterTestFile(t, applyPlanFile(root))

	if err := model.roster.SetPreset("quiet"); err != nil {
		t.Fatalf("SetPreset: %v", err)
	}
	model = model.refreshRoster()

	model = send(t, model, keyMsg("A"))
	if model.dialog != dialogApply {
		t.Fatalf("A opened dialog %v", model.dialog)
	}
	if body := strings.Join(model.dialogLines, "\n"); !strings.Contains(body, "executor: pi | codex/gpt-6-luna | high → -") {
		t.Fatalf("the dialog does not show the removed executor key:\n%s", body)
	}
	model = send(t, model, keyMsg("enter"))
	if model.dialog != dialogApply {
		t.Fatalf("the refused apply closed the dialog")
	}
	if body := strings.Join(model.dialogLines, "\n"); !strings.Contains(body, "needs an executor route") {
		t.Fatalf("the dialog does not name the missing chain:\n%s", body)
	}
	if got := readRosterTestFile(t, applyPlanFile(root)); got != before {
		t.Fatalf("a refused apply rewrote the plan:\n%s", got)
	}
}

// TestApplyDialogRefusesARunLock: the run lock stops the write at the
// action, the dialog keeps the reason and the plan keeps its header.
func TestApplyDialogRefusesARunLock(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := applyFixture(t)
	model := applyModel(t, root, rosterPath)
	before := readRosterTestFile(t, applyPlanFile(root))

	output, err := exec.Command("git", "-C", root, "rev-parse", "--git-dir").Output()
	if err != nil {
		t.Fatalf("git rev-parse --git-dir: %v", err)
	}
	gitDir := strings.TrimSpace(string(output))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	writeRosterTestFile(t, filepath.Join(gitDir, "docflow", "run", "run.lock"), "123\n")

	model = send(t, model, keyMsg("A"))
	model = send(t, model, keyMsg("enter"))
	if model.dialog != dialogApply {
		t.Fatalf("the refused apply closed the dialog")
	}
	if body := strings.Join(model.dialogLines, "\n"); !strings.Contains(body, "a run is in progress") {
		t.Fatalf("the dialog does not name the run lock:\n%s", body)
	}
	if got := readRosterTestFile(t, applyPlanFile(root)); got != before {
		t.Fatalf("a run lock did not stop the write:\n%s", got)
	}
}

// TestApplyDialogRefusesALiveRun: the workspace stops the write of its own
// live run before the file is touched.
func TestApplyDialogRefusesALiveRun(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := applyFixture(t)
	model := applyModel(t, root, rosterPath)
	before := readRosterTestFile(t, applyPlanFile(root))

	model.runState = runner.StateRunning
	model = send(t, model, keyMsg("A"))
	body := strings.Join(model.dialogLines, "\n")
	if model.dialog != dialogApply || !strings.Contains(body, "идёт прогон") {
		t.Fatalf("the live run is not named in the dialog: %v\n%s", model.dialog, body)
	}
	model = send(t, model, keyMsg("enter"))
	if model.dialog != dialogApply {
		t.Fatalf("the refused apply closed the dialog")
	}
	if got := readRosterTestFile(t, applyPlanFile(root)); got != before {
		t.Fatalf("a live run did not stop the write:\n%s", got)
	}
}

// TestApplyDialogRestoresThePlanAfterLint: a plan lint rejects is written,
// checked and put back; the dialog names the restore and the file keeps its
// text.
func TestApplyDialogRestoresThePlanAfterLint(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := applyFixture(t)
	// The missing Context section is a lint error unrelated to the header:
	// the write happens, lint rejects the plan and apply restores it.
	broken := strings.Replace(applyPlanText(), "## Context\n\n", "", 1)
	writeRosterTestFile(t, applyPlanFile(root), broken)
	model := applyModel(t, root, rosterPath)

	model = send(t, model, keyMsg("A"))
	model = send(t, model, keyMsg("enter"))
	if model.dialog != dialogApply {
		t.Fatalf("the lint refusal closed the dialog")
	}
	body := strings.Join(model.dialogLines, "\n")
	for _, want := range []string{"roster apply wrote an invalid plan; the plan is restored", "missing section 'Context'"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the dialog lost %q:\n%s", want, body)
		}
	}
	if got := readRosterTestFile(t, applyPlanFile(root)); got != broken {
		t.Fatalf("the restored plan differs from the original:\n%s", got)
	}
}

// TestPlanCardWarnsAboutTheActiveVariant: with a preset in force the plan
// card names the variant and the number of keys it rewrites; the warning
// goes away once the header is written.
func TestPlanCardWarnsAboutTheActiveVariant(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := applyFixture(t)
	model := applyModel(t, root, rosterPath)

	if err := model.roster.SetPreset("codex-first"); err != nil {
		t.Fatalf("SetPreset: %v", err)
	}
	model = model.refreshRoster()
	rendered := resize(t, model, 120, 40).String()
	if !strings.Contains(rendered, "⚠ вариант codex-first меняет 4 ключей") {
		t.Fatalf("the plan card lost the drift warning:\n%s", rendered)
	}

	model = send(t, model, keyMsg("A"))
	if body := strings.Join(model.dialogLines, "\n"); !strings.Contains(body, "вариант: codex-first") {
		t.Fatalf("the dialog does not name the active variant:\n%s", body)
	}
	updated, cmd := model.Update(keyMsg("enter"))
	model = updated.(Model)
	if model.dialog != dialogNone {
		t.Fatalf("Enter kept the dialog: %q", model.notice)
	}
	model = applyLoad(t, model, cmd)
	if rendered := resize(t, model, 120, 40).String(); strings.Contains(rendered, "⚠ вариант") {
		t.Fatalf("the card still warns after the header was written:\n%s", rendered)
	}
}

// TestApplyKeyIsNotBoundOnTheStreamAndHistory: A belongs to the two plan
// screens; the stream and the placeholder keep their own keys.
func TestApplyKeyIsNotBoundOnTheStreamAndHistory(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := applyFixture(t)
	model := applyModel(t, root, rosterPath)
	for _, target := range []screen{screenRun, screenHistory} {
		model = model.switchScreen(target)
		model = send(t, model, keyMsg("A"))
		if model.dialog != dialogNone {
			t.Fatalf("%v opened the apply dialog on A", target)
		}
	}
	if model.applyPlan != "" {
		t.Fatalf("A on %v remembered plan %q", model.screen, model.applyPlan)
	}
}

// TestPlanLoadStartsTheRosterRead: the plan load starts the roster read in
// the background once, so the plan card knows the active variant before the
// Ростер screen was ever opened.
func TestPlanLoadStartsTheRosterRead(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := applyFixture(t)
	model := New(root)
	model.lockCheck = func(string) (int, bool, error) { return 0, false, nil }
	model.rosterPath = rosterPath

	updated, cmd := model.Update(model.Init()())
	model = updated.(Model)
	if cmd == nil || !model.rosterPending {
		t.Fatalf("the plan load did not start the roster read: cmd=%v pending=%t", cmd != nil, model.rosterPending)
	}
	updated, _ = model.Update(cmd())
	model = updated.(Model)
	if model.rosterPhase != rosterReady || model.rosterPending {
		t.Fatalf("the roster read left phase=%v pending=%t error=%q", model.rosterPhase, model.rosterPending, model.rosterErr)
	}
	if model.roster == nil || len(model.roster.Variants) == 0 {
		t.Fatalf("the loaded workspace = %+v", model.roster)
	}
	// A second plan load keeps the loaded roster instead of reading it again.
	updated, again := model.Update(snapshotMsg{snapshot: model.snapshot})
	if again != nil || updated.(Model).rosterPending {
		t.Fatalf("a second plan load started another roster read")
	}
}
