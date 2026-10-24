package roster

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bsv-docflow/internal/clierrors"
)

// applyPlan is a plan with one Executor unit and one Role unit; the
// stale review key must go, the other keys keep their place.
func applyPlan(status string) string {
	return `---
id: P040
title: Roster apply
type: feature
status: ` + status + `
created: 2026-10-12
gate: true
review: pi | zai/m | high; codex | c | high
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
Executor: pi | zai/m | high
Commit: P040.1.1: first

### [ ] P040.1.2 Second unit
Read:
- a.py
Do:
- work
Verify: ` + "`true`" + `
Role: heavy
Commit: P040.1.2: second
`
}

func makeApplyProject(t *testing.T, rel, plan string) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"docs/plans", "docs/archive"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"docs/docflow.md": "---\ndocflow: 3\nread_budget: 40000\n---\n## Queue\n- P040\n\n## Backlog\n",
		"a.py":            "x = 1\n",
	}
	if rel == "" {
		files["docs/plans/P040-roster.md"] = plan
	} else {
		files[rel] = plan
	}
	for path, text := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
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
	return root
}

func applyProject(t *testing.T, plan string) string {
	t.Helper()
	return makeApplyProject(t, "", plan)
}

func gitDir(t *testing.T, root string) string {
	t.Helper()
	output, err := exec.Command("git", "-C", root, "rev-parse", "--git-dir").Output()
	if err != nil {
		t.Fatalf("git rev-parse --git-dir: %v", err)
	}
	dir := strings.TrimSpace(string(output))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	return dir
}

func wantApplyError(t *testing.T, err error, code int, text string) {
	t.Helper()
	if err == nil {
		t.Fatalf("Apply() = nil, want error with %q", text)
	}
	if !strings.Contains(err.Error(), text) {
		t.Fatalf("Apply() error = %v, want it to name %q", err, text)
	}
	if coded, ok := err.(interface{ ExitCode() int }); !ok || coded.ExitCode() != code {
		t.Fatalf("exit code of %v is not %d", err, code)
	}
}

func TestApplyWritesAndRemovesKeys(t *testing.T) {
	root := applyProject(t, applyPlan("active"))
	result, err := Apply(root, "P040", "", "")
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Code != clierrors.EXIT_OK {
		t.Fatalf("Apply() code = %d, stderr %s", result.Code, result.Stderr)
	}
	want := `executor: - → pi | zai/glm-5.3-flash | max
fallback: - → codex | gpt-6-luna | max; claude | claude-sonnet-5-5 | high
heavy: - → pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high
review: pi | zai/m | high; codex | c | high → -
replan: - → claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
commit: P040: apply roster
`
	if result.Stdout != want {
		t.Fatalf("Apply() stdout =\n%s\nwant\n%s", result.Stdout, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Apply() stderr =\n%s\nwant empty", result.Stderr)
	}
	text, err := os.ReadFile(filepath.Join(root, "docs", "plans", "P040-roster.md"))
	if err != nil {
		t.Fatal(err)
	}
	wantPlan := `---
id: P040
title: Roster apply
type: feature
status: active
created: 2026-10-12
gate: true
executor: pi | zai/glm-5.3-flash | max
fallback: codex | gpt-6-luna | max; claude | claude-sonnet-5-5 | high
heavy: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high
replan: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
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
Executor: pi | zai/m | high
Commit: P040.1.1: first

### [ ] P040.1.2 Second unit
Read:
- a.py
Do:
- work
Verify: ` + "`true`" + `
Role: heavy
Commit: P040.1.2: second
`
	if string(text) != wantPlan {
		t.Fatalf("plan after apply =\n%s\nwant\n%s", string(text), wantPlan)
	}
}

func TestApplyIdempotent(t *testing.T) {
	root := applyProject(t, applyPlan("active"))
	if _, err := Apply(root, "P040", "", ""); err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}
	path := filepath.Join(root, "docs", "plans", "P040-roster.md")
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Apply(root, "P040", "", "")
	if err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}
	if result.Code != clierrors.EXIT_NOTHING {
		t.Fatalf("second Apply() code = %d, want %d (stdout %q)", result.Code, clierrors.EXIT_NOTHING, result.Stdout)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(after) {
		t.Fatalf("second Apply() changed the plan")
	}
}

func TestApplyProfile(t *testing.T) {
	root := applyProject(t, applyPlan("active"))
	rosterPath := filepath.Join(t.TempDir(), "roster.toml")
	if err := os.WriteFile(rosterPath, []byte(draft+"\n[profile.codex-first]\nspend = [\"codex\", \"claude\", \"zai\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Apply(root, "P040", "codex-first", rosterPath)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Code != clierrors.EXIT_OK {
		t.Fatalf("Apply() code = %d, stderr %s", result.Code, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "executor: - → codex | gpt-6-luna | max\n") {
		t.Fatalf("Apply() stdout =\n%s\nwant the codex executor", result.Stdout)
	}
	text, err := os.ReadFile(filepath.Join(root, "docs", "plans", "P040-roster.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "\nheavy: codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high; pi | zai/glm-5.3 | max\n"
	if !strings.Contains(string(text), want) {
		t.Fatalf("plan after profile apply =\n%s\nwant the profile heavy chain %q", string(text), want)
	}
}

func TestApplyStatusAndArchive(t *testing.T) {
	t.Run("done status", func(t *testing.T) {
		root := applyProject(t, applyPlan("done"))
		path := filepath.Join(root, "docs", "plans", "P040-roster.md")
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Apply(root, "P040", "", "")
		wantApplyError(t, err, clierrors.EXIT_FORMAT, "plan is done")
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("Apply() changed the plan")
		}
	})
	t.Run("archived plan", func(t *testing.T) {
		root := makeApplyProject(t, "docs/archive/P040-roster.md", applyPlan("done"))
		_, err := Apply(root, "P040", "", "")
		wantApplyError(t, err, clierrors.EXIT_FORMAT, "no plan in docs/plans/")
	})
}

func TestApplyRunLock(t *testing.T) {
	root := applyProject(t, applyPlan("active"))
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
	_, err = Apply(root, "P040", "", "")
	wantApplyError(t, err, clierrors.EXIT_ORDER, "a run is in progress")
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("Apply() changed the plan")
	}
}

func TestApplyRestoresOnLintError(t *testing.T) {
	// Model: units conflict with the executor assignment the roster
	// writes, so the lint after the write fails and the plan is restored.
	plan := strings.Replace(applyPlan("active"), "Executor: pi | zai/m | high", "Model: strong", 1)
	plan = strings.Replace(plan, "Role: heavy\n", "", 1)
	root := applyProject(t, plan)
	path := filepath.Join(root, "docs", "plans", "P040-roster.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Apply(root, "P040", "", "")
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Code != clierrors.EXIT_FORMAT {
		t.Fatalf("Apply() code = %d, want %d", result.Code, clierrors.EXIT_FORMAT)
	}
	if !strings.Contains(result.Stderr, "Model: conflicts with executors") {
		t.Fatalf("Apply() stderr =\n%s\nwant the lint error", result.Stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("Apply() did not restore the plan:\n%s", string(after))
	}
}
