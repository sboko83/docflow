package roster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/cycle"
)

// applyV2Roster loads the v2 fixture with the expiry pushed past the test
// clock, the low preset in memory and the scenario catalog. The policy
// file is never the personal roster.
func applyV2Roster(t *testing.T) (*Policy, *State, *Catalog) {
	t.Helper()
	data, err := os.ReadFile("testdata/policy-v2.toml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(data), `expires = "2026-06-30"`, `expires = "2099-01-01"`, 1)
	if text == string(data) {
		t.Fatal("policy-v2.toml no longer names the zai expiry the test patches")
	}
	path := filepath.Join(t.TempDir(), "roster.toml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	policy, err := LoadPolicy(path)
	if err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}
	state := NewState("")
	state.Preset = "low"
	catalog, err := LoadCatalog("testdata/catalog-scenario.json")
	if err != nil {
		t.Fatalf("LoadCatalog() error = %v", err)
	}
	return policy, state, catalog
}

// applyV2Plan is a plan with the three read roles, a stale heavy key and an
// explicit Executor unit: apply must write the used role keys, remove the
// heavy key and leave the unit assignment alone.
func applyV2Plan() string {
	return `---
id: P040
title: Roster v2 apply
type: feature
status: active
created: 2026-10-12
gate: true
heavy: pi | zai/glm-5.3 | max
---
## Goal
Цель плана.

## Context

## Constraints
Ограничение.

## Prerequisites

## Traps
Ловушка плана.

## Decisions
- решение — почему — альтернатива

## Phase 1: One

### [ ] P040.1.1 First unit
Read:
- a.py
Do:
- work
Verify: ` + "`true`" + `
Executor: pi | zai/glm-5.3-flash | low
Commit: P040.1.1: first

### [ ] P040.1.2 Second unit
Read:
- a.py
Do:
- work
Verify: ` + "`true`" + `
Role: cheap
Commit: P040.1.2: second

### [ ] P040.1.3 Third unit
Read:
- a.py
Do:
- work
Verify: ` + "`true`" + `
Role: review
Commit: P040.1.3: third

### [x] P040.1.4 Done unit
Read:
- a.py
Do:
- work
Verify: ` + "`true`" + `
Role: cheap
Commit: P040.1.4: done
`
}

// applyV2Header is the header the low preset must write into the plan.
var applyV2Header = map[string]string{
	"executor": "pi | opencode-go/deepseek-v4.1 | high",
	"cheap":    "pi | opencode-go/deepseek-v4.1-flash | high; pi | zai/glm-5.3-flash | low",
	"review":   "pi | opencode-go/deepseek-v4.1 | high",
	"replan":   "codex | gpt-6.1-sol | medium",
}

func readApplyPlan(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "docs", "plans", "P040-roster.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestApplyV2WritesHeader(t *testing.T) {
	policy, state, catalog := applyV2Roster(t)
	root := applyProject(t, applyV2Plan())
	result, report, err := ApplyV2(root, "P040", policy, state, catalog, false)
	if err != nil {
		t.Fatalf("ApplyV2() error = %v", err)
	}
	if result.Code != clierrors.EXIT_OK {
		t.Fatalf("ApplyV2() code = %d, stderr:\n%s", result.Code, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "commit: P040: apply roster\n") {
		t.Fatalf("ApplyV2() stdout =\n%s\nwant the commit hint", result.Stdout)
	}
	if !strings.Contains(result.Stderr, "no fallback for executor") {
		t.Fatalf("ApplyV2() stderr =\n%s\nwant the missing fallback warning", result.Stderr)
	}
	text := readApplyPlan(t, root)
	for key, want := range applyV2Header {
		if line := key + ": " + want; !strings.Contains(text, "\n"+line+"\n") {
			t.Errorf("plan after apply has no %q:\n%s", line, text)
		}
	}
	if strings.Contains(text, "heavy:") {
		t.Errorf("plan after apply kept the role key without units:\n%s", text)
	}
	if !strings.Contains(text, "Executor: pi | zai/glm-5.3-flash | low\n") {
		t.Errorf("apply rewrote the unit Executor:\n%s", text)
	}
	if strings.Contains(text, "Model:") {
		t.Errorf("apply wrote a Model: field:\n%s", text)
	}
	if len(report.Changes) != 5 {
		t.Fatalf("changes = %+v, want executor, cheap, heavy, review and replan", report.Changes)
	}
	if change := report.Changes[2]; change.Key != "heavy" || change.Old == "" || change.New != "" {
		t.Errorf("heavy change = %+v, want a removal of the stale chain", change)
	}
	// The written header is exactly what the brief of the same roles gives:
	// apply must not render routes its own way.
	plan, err := cycle.FindPlan(root, "P040")
	if err != nil {
		t.Fatal(err)
	}
	brief, err := BuildBrief(policy, state, catalog, PlanRoles(plan))
	if err != nil {
		t.Fatalf("BuildBrief() error = %v", err)
	}
	for key, want := range brief.Header() {
		if got := plan.Meta[key]; got != want {
			t.Errorf("header %s = %q, want the brief value %q", key, got, want)
		}
	}
}

func TestApplyV2Idempotent(t *testing.T) {
	policy, state, catalog := applyV2Roster(t)
	root := applyProject(t, applyV2Plan())
	if _, _, err := ApplyV2(root, "P040", policy, state, catalog, false); err != nil {
		t.Fatalf("first ApplyV2() error = %v", err)
	}
	after := readApplyPlan(t, root)
	result, report, err := ApplyV2(root, "P040", policy, state, catalog, false)
	if err != nil {
		t.Fatalf("second ApplyV2() error = %v", err)
	}
	if result.Code != clierrors.EXIT_NOTHING {
		t.Fatalf("second ApplyV2() code = %d, want %d (stdout %q)", result.Code, clierrors.EXIT_NOTHING, result.Stdout)
	}
	if len(report.Changes) != 0 {
		t.Fatalf("second ApplyV2() changes = %+v, want none", report.Changes)
	}
	if again := readApplyPlan(t, root); again != after {
		t.Fatalf("second ApplyV2() changed the plan")
	}
}

func TestApplyV2DryRun(t *testing.T) {
	policy, state, catalog := applyV2Roster(t)
	root := applyProject(t, applyV2Plan())
	before := readApplyPlan(t, root)
	result, report, err := ApplyV2(root, "P040", policy, state, catalog, true)
	if err != nil {
		t.Fatalf("ApplyV2() error = %v", err)
	}
	if result.Code != clierrors.EXIT_OK {
		t.Fatalf("ApplyV2() code = %d, stderr:\n%s", result.Code, result.Stderr)
	}
	if !report.DryRun || len(report.Changes) == 0 {
		t.Fatalf("report = %+v, want a dry run with changes", report)
	}
	if !strings.Contains(result.Stdout, "executor: - → pi | opencode-go/deepseek-v4.1 | high\n") {
		t.Fatalf("ApplyV2() stdout =\n%s\nwant the executor change", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "dry-run: P040: plan not written\n") {
		t.Fatalf("ApplyV2() stdout =\n%s\nwant the dry-run note", result.Stdout)
	}
	if after := readApplyPlan(t, root); after != before {
		t.Fatalf("dry run changed the plan:\n%s", after)
	}
}

func TestApplyV2Warnings(t *testing.T) {
	policy, state, catalog := applyV2Roster(t)
	plan := strings.Replace(applyV2Plan(), "Executor: pi | zai/glm-5.3-flash | low", "Executor: claude | claude-opus-5-5 | high", 1)
	root := applyProject(t, plan)
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(strings.Repeat("x", 700000)), 0o644); err != nil {
		t.Fatal(err)
	}
	plan = strings.Replace(plan, "### [ ] P040.1.2 Second unit\nRead:\n- a.py", "### [ ] P040.1.2 Second unit\nRead:\n- big.txt", 1)
	if err := os.WriteFile(filepath.Join(root, "docs", "plans", "P040-roster.md"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	result, _, err := ApplyV2(root, "P040", policy, state, catalog, true)
	if err != nil {
		t.Fatalf("ApplyV2() error = %v", err)
	}
	if result.Code != clierrors.EXIT_OK {
		t.Fatalf("warnings changed the exit code: %d\n%s", result.Code, result.Stderr)
	}
	for _, want := range []string{
		"unit P040.1.1: Executor: claude | claude-opus-5-5 | high is outside the current conditions: access claude is off",
		"unit P040.1.2: Read: proxy ~175000 tokens > cheap budget 152371",
		"no fallback for executor",
	} {
		if !strings.Contains(result.Stderr, want) {
			t.Errorf("stderr =\n%s\nwant it to name %q", result.Stderr, want)
		}
	}
}

func TestApplyV2RestoresOnLintError(t *testing.T) {
	policy, state, catalog := applyV2Roster(t)
	plan := strings.Replace(applyV2Plan(), "Executor: pi | zai/glm-5.3-flash | low", "Model: strong", 1)
	root := applyProject(t, plan)
	path := filepath.Join(root, "docs", "plans", "P040-roster.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result, _, err := ApplyV2(root, "P040", policy, state, catalog, false)
	if err != nil {
		t.Fatalf("ApplyV2() error = %v", err)
	}
	if result.Code != clierrors.EXIT_FORMAT {
		t.Fatalf("ApplyV2() code = %d, want %d", result.Code, clierrors.EXIT_FORMAT)
	}
	if !strings.Contains(result.Stderr, "Model: conflicts with executors") {
		t.Fatalf("ApplyV2() stderr =\n%s\nwant the lint error", result.Stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("ApplyV2() did not restore the plan:\n%s", string(after))
	}
}

func TestApplyV2RunLock(t *testing.T) {
	policy, state, catalog := applyV2Roster(t)
	root := applyProject(t, applyV2Plan())
	path := filepath.Join(root, "docs", "plans", "P040-roster.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(gitDir(t, root), "docflow", "run", "run.lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, []byte("123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err = ApplyV2(root, "P040", policy, state, catalog, false)
	wantApplyError(t, err, clierrors.EXIT_ORDER, "a run is in progress")
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("ApplyV2() changed the plan")
	}
}
