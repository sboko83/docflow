package cycle

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bsv-docflow/internal/clierrors"
)

func cycleDocflow(queue ...string) string {
	var items strings.Builder
	for _, id := range queue {
		fmt.Fprintf(&items, "- %s\n", id)
	}
	return "---\ndocflow: 3\nread_budget: 40000\n---\n## Queue\n" + items.String() + "\n## Backlog\n"
}

func cycleUnit(id, status string, depends ...string) string {
	dep := ""
	if len(depends) > 0 {
		dep = "Depends: " + depends[0] + "\n"
	}
	return fmt.Sprintf("### [%s] %s Unit %s\nRead:\n- a.py\nDo:\n- b\nVerify: `true`\n%sCommit: %s: unit\n\n", status, id, id, dep, id)
}

func cyclePlan(id, status string, units ...string) string {
	return fmt.Sprintf("---\nid: %s\ntitle: Plan %s\ntype: feature\nstatus: %s\ncreated: 2026-10-12\n---\n", id, id, status) +
		"## Goal\nЦель плана.\n\n## Context\n\n## Constraints\nОграничение.\n\n## Prerequisites\n- Симулятор запущен\n\n## Traps\nЛовушка плана.\n\n## Decisions\n- решение — почему — альтернатива\n\n## Phase 1: First\n\n" + strings.Join(units, "")
}

func makeCycleProject(t *testing.T, docflow string, plans map[string]string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"docs/plans", "docs/archive"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeCycleFile(t, root, "docs/docflow.md", docflow)
	for id, text := range plans {
		writeCycleFile(t, root, "docs/plans/"+id+"-plan.md", text)
	}
	for path, text := range files {
		writeCycleFile(t, root, path, text)
	}
	gitCycle(t, root, "init", "-q")
	gitCycle(t, root, "config", "user.name", "docflow test")
	gitCycle(t, root, "config", "user.email", "test@example.invalid")
	gitCycle(t, root, "add", "-A")
	gitCycle(t, root, "commit", "-q", "-m", "initial")
	return root
}

func writeCycleFile(t *testing.T, root, path, text string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitCycle(t *testing.T, root string, args ...string) string {
	t.Helper()
	return gitCycleDir(t, root, args...)
}

func gitCycleDir(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = cwd
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
	return string(output)
}

func selectionFor(t *testing.T, root string) Selection {
	t.Helper()
	docflow, err := readDocflow(root)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := SelectUnit(root, docflow)
	if err != nil {
		t.Fatal(err)
	}
	return selection
}

func TestSelectUnit(t *testing.T) {
	tests := []struct {
		name       string
		queue      []string
		plans      map[string]string
		kind, unit string
		plan       string
	}{
		{
			name: "first ready todo", queue: []string{"P001"},
			plans: map[string]string{"P001": cyclePlan("P001", "active", cycleUnit("P001.1.1", "x"), cycleUnit("P001.1.2", " "))},
			kind:  "unit", unit: "P001.1.2", plan: "P001",
		},
		{
			name: "doing wins over todo", queue: []string{"P001", "P002"},
			plans: map[string]string{
				"P001": cyclePlan("P001", "active", cycleUnit("P001.1.1", " "), cycleUnit("P001.1.2", " ")),
				"P002": cyclePlan("P002", "active", cycleUnit("P002.1.1", "~")),
			},
			kind: "unit", unit: "P002.1.1", plan: "P002",
		},
		{
			name: "depends not done", queue: []string{"P001"},
			plans: map[string]string{"P001": cyclePlan("P001", "active", cycleUnit("P001.1.1", "x"), cycleUnit("P001.1.2", " ", "P001.1.3"), cycleUnit("P001.1.3", " ", "-"))},
			kind:  "unit", unit: "P001.1.3", plan: "P001",
		},
		{
			name: "implicit depends across phases", queue: []string{"P001"},
			plans: map[string]string{"P001": cyclePlan("P001", "active", cycleUnit("P001.1.1", "x"), cycleUnit("P001.1.2", " "), cycleUnit("P001.2.1", "x", "-"))},
			kind:  "unit", unit: "P001.1.2", plan: "P001",
		},
		{
			name: "skipped counts as done", queue: []string{"P001"},
			plans: map[string]string{"P001": cyclePlan("P001", "active", cycleUnit("P001.1.1", "-"), cycleUnit("P001.1.2", " "))},
			kind:  "unit", unit: "P001.1.2", plan: "P001",
		},
		{
			name: "no ready unit blocks queue", queue: []string{"P001", "P002"},
			plans: map[string]string{
				"P001": cyclePlan("P001", "active", cycleUnit("P001.1.1", " ", "P001.1.2"), cycleUnit("P001.1.2", " ", "P001.1.1")),
				"P002": cyclePlan("P002", "active", cycleUnit("P002.1.1", " ")),
			},
			kind: "nothing", plan: "P001",
		},
		{
			name: "draft skipped", queue: []string{"P001", "P002"},
			plans: map[string]string{
				"P001": cyclePlan("P001", "draft", cycleUnit("P001.1.1", "~")),
				"P002": cyclePlan("P002", "active", cycleUnit("P002.1.1", " ")),
			},
			kind: "unit", unit: "P002.1.1", plan: "P002",
		},
		{
			name: "block", queue: []string{"P001"},
			plans: map[string]string{"P001": cyclePlan("P001", "active", cycleUnit("P001.1.1", "x"), cycleUnit("P001.1.2", "!"), cycleUnit("P001.1.3", " ", "-"))},
			kind:  "blocked", unit: "P001.1.2", plan: "P001",
		},
		{
			name: "blocked plan wins before later todo", queue: []string{"P001", "P002"},
			plans: map[string]string{
				"P001": cyclePlan("P001", "active", cycleUnit("P001.1.1", "x"), cycleUnit("P001.1.2", "!")),
				"P002": cyclePlan("P002", "active", cycleUnit("P002.1.1", " ")),
			},
			kind: "blocked", unit: "P001.1.2", plan: "P001",
		},
		{
			name: "ready to close plan skipped", queue: []string{"P001", "P002"},
			plans: map[string]string{
				"P001": cyclePlan("P001", "active", cycleUnit("P001.1.1", "x"), cycleUnit("P001.1.2", "-")),
				"P002": cyclePlan("P002", "active", cycleUnit("P002.1.1", " ")),
			},
			kind: "unit", unit: "P002.1.1", plan: "P002",
		},
		{
			name: "empty queue", queue: []string{}, plans: map[string]string{}, kind: "nothing",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := makeCycleProject(t, cycleDocflow(test.queue...), test.plans, map[string]string{"a.py": "print(1)\n"})
			got := selectionFor(t, root)
			if got.Kind != test.kind {
				t.Fatalf("selection kind = %q, want %q", got.Kind, test.kind)
			}
			if test.unit != "" && (got.Unit == nil || got.Unit.ID != test.unit) {
				t.Fatalf("selection unit = %#v, want %s", got.Unit, test.unit)
			}
			if test.plan != "" && (got.Plan == nil || got.Plan.Meta["id"] != test.plan) {
				t.Fatalf("selection plan = %#v, want %s", got.Plan, test.plan)
			}
		})
	}
}

func TestNextExitAndText(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		root := makeCycleProject(t, cycleDocflow("P001"), map[string]string{"P001": cyclePlan("P001", "active", cycleUnit("P001.1.1", " "))}, nil)
		got, err := Next(root)
		if err != nil || got.Code != 0 || got.Stdout != "P001.1.1\n" || got.Stderr != "" {
			t.Fatalf("Next() = %#v, %v", got, err)
		}
	})
	t.Run("blocked", func(t *testing.T) {
		root := makeCycleProject(t, cycleDocflow("P001"), map[string]string{"P001": cyclePlan("P001", "active", cycleUnit("P001.1.1", "!"))}, nil)
		got, err := Next(root)
		if err != nil || got.Code != 3 || got.Stdout != "P001.1.1\n" {
			t.Fatalf("Next() = %#v, %v", got, err)
		}
	})
	t.Run("nothing", func(t *testing.T) {
		root := makeCycleProject(t, cycleDocflow(), nil, nil)
		got, err := Next(root)
		if err != nil || got.Code != 1 || got.Stdout != "" || got.Stderr != "nothing to do\n" {
			t.Fatalf("Next() = %#v, %v", got, err)
		}
	})
	t.Run("missing plan", func(t *testing.T) {
		root := makeCycleProject(t, cycleDocflow("P009"), nil, nil)
		_, err := Next(root)
		if err == nil || !strings.Contains(err.Error(), "P009: no plan in docs/plans/") {
			t.Fatalf("Next() error = %v", err)
		}
	})
}

func TestStatusLines(t *testing.T) {
	root := makeCycleProject(t, cycleDocflow("P001", "P002", "P003"), map[string]string{
		"P001": cyclePlan("P001", "active", cycleUnit("P001.1.1", "x"), cycleUnit("P001.1.2", "-")),
		"P002": cyclePlan("P002", "active", cycleUnit("P002.1.1", "x"), cycleUnit("P002.1.2", "~"), cycleUnit("P002.1.3", " ")),
		"P003": cyclePlan("P003", "draft", cycleUnit("P003.1.1", " ")),
	}, nil)
	result, err := Status(root, "lint: 3 warnings")
	if err != nil {
		t.Fatal(err)
	}
	want := "P001 Plan P001 [active] 2/2 ready to close\n" +
		"P002 Plan P002 [active] 1/3\n" +
		"P003 Plan P003 [draft] 0/1\n" +
		"current: P002.1.2 Unit P002.1.2\n" +
		"lint: 3 warnings\n"
	if result.Code != 0 || result.Stdout != want || result.Stderr != "" {
		t.Fatalf("Status() = %#v, want stdout %q", result, want)
	}
}

func TestStatusReadinessAndBlocked(t *testing.T) {
	t.Run("doing is not ready to close", func(t *testing.T) {
		root := makeCycleProject(t, cycleDocflow("P001", "P002"), map[string]string{
			"P001": cyclePlan("P001", "active", cycleUnit("P001.1.1", "x"), cycleUnit("P001.1.2", "~")),
			"P002": cyclePlan("P002", "active", cycleUnit("P002.1.1", "x"), cycleUnit("P002.1.2", "-")),
		}, nil)
		result, err := Status(root, "lint: ok")
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n")
		if lines[0] != "P001 Plan P001 [active] 1/2" || lines[1] != "P002 Plan P002 [active] 2/2 ready to close" {
			t.Fatalf("status lines = %#v", lines)
		}
	})
	t.Run("blocked selection", func(t *testing.T) {
		root := makeCycleProject(t, cycleDocflow("P001"), map[string]string{"P001": cyclePlan("P001", "active", cycleUnit("P001.1.1", "x"), cycleUnit("P001.1.2", "!"))}, nil)
		result, err := Status(root, "lint: 1 warnings")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result.Stdout, "blocked: P001.1.2 Unit P001.1.2\n") {
			t.Fatalf("status = %q", result.Stdout)
		}
	})
	t.Run("empty queue", func(t *testing.T) {
		root := makeCycleProject(t, cycleDocflow(), nil, nil)
		result, err := Status(root, "lint: ok")
		if err != nil || result.Stdout != "queue is empty\ncurrent: none\nlint: ok\n" {
			t.Fatalf("Status() = %#v, %v", result, err)
		}
	})
}

func TestCommitPendingStateIsSelectedAndReported(t *testing.T) {
	for _, state := range []string{"committing", "verified"} {
		t.Run(state, func(t *testing.T) {
			root := makeCycleProject(t, cycleDocflow("P001"), map[string]string{"P001": cyclePlan("P001", "active", cycleUnit("P001.1.1", " "))}, nil)
			planPath := filepath.Join(root, "docs", "plans", "P001-plan.md")
			planText, err := os.ReadFile(planPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(planPath, []byte(strings.Replace(string(planText), "### [ ] P001.1.1", "### [x] P001.1.1", 1)), 0o644); err != nil {
				t.Fatal(err)
			}
			statePath := filepath.Join(root, ".git", "docflow", state)
			if err := os.MkdirAll(statePath, 0o755); err != nil {
				t.Fatal(err)
			}
			filename := "P001.1.1"
			if state == "committing" {
				filename += ".json"
			}
			if err := os.WriteFile(filepath.Join(statePath, filename), []byte("{}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			selection := selectionFor(t, root)
			if selection.Kind != "unit" || selection.Unit == nil || selection.Unit.Status != "x" {
				t.Fatalf("selection = %#v", selection)
			}
			status, err := Status(root, "lint: ok")
			if err != nil || !strings.Contains(status.Stdout, "P001 Plan P001 [active] 0/1\n") || !strings.Contains(status.Stdout, "current: P001.1.1 Unit P001.1.1 (commit pending)\n") {
				t.Fatalf("Status() = %#v, %v", status, err)
			}
			prompt, err := runPrompt(t, root, "")
			if err != nil || prompt.Code != 5 || !strings.Contains(prompt.Stderr, "P001.1.1: commit pending") {
				t.Fatalf("Prompt() = %#v, %v", prompt, err)
			}
		})
	}
}

const promptDocflow = "---\ndocflow: 3\nread_budget: 40000\n---\n## Queue\n- P001\n\n## Backlog\n"

const promptPlan = `---
id: P001
title: Plan
type: feature
status: active
created: 2026-10-12
---
## Goal
Цель плана.

## Context
- docs/ctx.md

## Constraints
Ограничение.

## Prerequisites
- Симулятор запущен

## Traps
Ловушка плана.

## Decisions
- решение — почему — альтернатива

## Phase 1: First

### [x] P001.1.1 First
Read:
- a.py
Do:
- b
Verify: ` + "`true`" + `
Commit: P001.1.1: first
Handoff:
- от первого

### [x] P001.1.2 Second
Read:
- a.py
Do:
- b
Verify: ` + "`true`" + `
Commit: P001.1.2: second
Handoff:
- от второго

### [ ] P001.1.3 Third
Read:
- a.py
Do:
- c
Verify: ` + "`true`" + `
Commit: P001.1.3: third

### [ ] P001.1.4 Fourth
Read:
- a.py
Do:
- d
Verify: ` + "`true`" + `
Commit: P001.1.4: fourth
`

func promptProject(t *testing.T, plan string, files map[string]string) string {
	t.Helper()
	if files == nil {
		files = map[string]string{}
	}
	if _, exists := files["a.py"]; !exists {
		files["a.py"] = "print(1)\n"
	}
	if _, exists := files["docs/ctx.md"]; !exists {
		files["docs/ctx.md"] = "# Ctx\n"
	}
	return makeCycleProject(t, promptDocflow, map[string]string{"P001": plan}, files)
}

func runPrompt(t *testing.T, root, unitID string) (Result, error) {
	t.Helper()
	return Prompt(root, unitID, nil)
}

func TestPromptTextAndReadBudget(t *testing.T) {
	root := promptProject(t, promptPlan, nil)
	result, err := runPrompt(t, root, "")
	if err != nil || result.Code != 0 {
		t.Fatalf("Prompt() = %#v, %v", result, err)
	}
	if !strings.HasPrefix(result.Stdout, "# P001.1.3 Third\n\nPlan: P001 Plan (docs/plans/P001-plan.md)\n") {
		t.Fatalf("prompt header = %q", result.Stdout[:min(len(result.Stdout), 90)])
	}
	var headings []string
	for _, line := range strings.Split(result.Stdout, "\n") {
		if strings.HasPrefix(line, "## ") {
			headings = append(headings, line)
		}
	}
	wantHeadings := []string{"## Goal", "## Constraints", "## Prerequisites", "## Traps", "## Context", "## Decisions", "## Unit", "## Handoff from Depends", "## Cycle"}
	if strings.Join(headings, "\n") != strings.Join(wantHeadings, "\n") {
		t.Fatalf("headings = %#v", headings)
	}
	for _, part := range []string{"Цель плана.", "- Симулятор запущен", "- решение — почему — альтернатива", "### [~] P001.1.3 Third\nRead:\n- a.py\nDo:\n- c\n", "### P001.1.2\n- от второго\n", "docflow verify P001.1.3", "docflow finish P001.1.3 --handoff", "--decision", "docflow block P001.1.3 --reason"} {
		if !strings.Contains(result.Stdout, part) {
			t.Errorf("prompt does not contain %q", part)
		}
	}
	if strings.Contains(result.Stdout, "от первого") || strings.Contains(result.Stdout, "P001.1.4") {
		t.Fatalf("prompt includes unrelated handoff or unit: %s", result.Stdout)
	}
	tokens := (len([]byte(result.Stdout)) + len("# Ctx\n") + len("print(1)\n")) / 4
	if result.Stderr != fmt.Sprintf("P001.1.3: ~%d tokens of context (prompt + Context + Read), read_budget 40000\n", tokens) {
		t.Fatalf("stderr = %q, tokens %d", result.Stderr, tokens)
	}
	statsPath := filepath.Join(root, ".git", "docflow", "stats", "P001.1.3.json")
	if data, err := os.ReadFile(statsPath); err != nil || !strings.Contains(string(data), fmt.Sprintf("\"proxy_tokens\": %d", tokens)) {
		t.Fatalf("prompt stats = %q, %v", data, err)
	}
}

func TestPromptArchiveGotchas(t *testing.T) {
	archive := func(id, gotcha, extra string) string {
		return fmt.Sprintf("---\nid: %s\ntitle: Old\nclosed: 2026-10-01\n%s---\n## Outcome\nЕсть.\n\n## Decisions\n- d — w — a\n\n## Gotchas\n- %s\n\n## Follow-ups\n- нет\n", id, extra, gotcha)
	}
	root := promptProject(t, promptPlan, map[string]string{
		"a.py":                     "См. archive/P000-old.md\nСм. archive/P002-new.md\nСм. archive/P005-gone.md\n",
		"docs/archive/P000-old.md": archive("P000", "ловушка из архива", ""),
		"docs/archive/P002-new.md": archive("P002", "устарело", "superseded_by: P003\n"),
	})
	result, err := runPrompt(t, root, "")
	if err != nil || result.Code != 0 {
		t.Fatalf("Prompt() = %#v, %v", result, err)
	}
	if !strings.Contains(result.Stdout, "## Archive gotchas\n\n- (P000) ловушка из архива\n\n## Cycle") || strings.Contains(result.Stdout, "устарело") {
		t.Fatalf("archive gotchas output = %s", result.Stdout)
	}
}

func TestPromptOmitsAbsentSections(t *testing.T) {
	t.Run("gotchas", func(t *testing.T) {
		root := promptProject(t, promptPlan, nil)
		result, err := runPrompt(t, root, "")
		if err != nil || strings.Contains(result.Stdout, "## Archive gotchas") {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
	})
	t.Run("prerequisites", func(t *testing.T) {
		plan := strings.Replace(promptPlan, "## Prerequisites\n- Симулятор запущен\n\n", "", 1)
		root := promptProject(t, plan, nil)
		result, err := runPrompt(t, root, "")
		if err != nil || strings.Contains(result.Stdout, "## Prerequisites") {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
	})
}

func TestPromptChangesOnlyUnitStatus(t *testing.T) {
	root := promptProject(t, promptPlan, nil)
	if result, err := runPrompt(t, root, ""); err != nil || result.Code != 0 {
		t.Fatalf("Prompt() = %#v, %v", result, err)
	}
	want := strings.Replace(promptPlan, "### [ ] P001.1.3", "### [~] P001.1.3", 1)
	data, err := os.ReadFile(filepath.Join(root, "docs", "plans", "P001-plan.md"))
	if err != nil || string(data) != want {
		t.Fatalf("saved plan differs: err=%v", err)
	}
}

func TestPromptRepeatIsResumedAndStable(t *testing.T) {
	root := promptProject(t, promptPlan, nil)
	first, err := runPrompt(t, root, "")
	if err != nil {
		t.Fatal(err)
	}
	writeCycleFile(t, root, "a.py", "print(2)\n")
	second, err := runPrompt(t, root, "")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := runPrompt(t, root, "P001.1.3")
	if err != nil {
		t.Fatal(err)
	}
	if first.Stdout != second.Stdout || first.Stdout != explicit.Stdout || first.Code != 0 || second.Code != 0 || explicit.Code != 0 {
		t.Fatalf("prompt outputs differ: first=%#v second=%#v explicit=%#v", first, second, explicit)
	}
	if strings.Contains(first.Stderr, "(resumed)") || !strings.Contains(second.Stderr, "P001.1.3 (resumed):") {
		t.Fatalf("resumed diagnostics: first=%q second=%q", first.Stderr, second.Stderr)
	}
}

func TestPromptDirtyTreeAndPlanExclusion(t *testing.T) {
	t.Run("untracked file", func(t *testing.T) {
		root := promptProject(t, promptPlan, nil)
		writeCycleFile(t, root, "new.txt", "x")
		result, err := runPrompt(t, root, "")
		if err != nil || result.Code != 5 || result.Stdout != "" || !strings.Contains(result.Stderr, "working tree is dirty") || !strings.Contains(result.Stderr, "new.txt") {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
		planText, _ := os.ReadFile(filepath.Join(root, "docs", "plans", "P001-plan.md"))
		if !strings.Contains(string(planText), "### [ ] P001.1.3") {
			t.Fatal("dirty prompt changed the unit status")
		}
	})
	t.Run("tracked file", func(t *testing.T) {
		root := promptProject(t, promptPlan, nil)
		writeCycleFile(t, root, "a.py", "print(3)\n")
		result, err := runPrompt(t, root, "")
		if err != nil || result.Code != 5 {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
	})
	t.Run("plan file excluded", func(t *testing.T) {
		plan := strings.Replace(promptPlan, "Цель плана.", "Цель плана!", 1)
		root := promptProject(t, plan, nil)
		result, err := runPrompt(t, root, "")
		if err != nil || result.Code != 0 || !strings.Contains(result.Stdout, "Цель плана!") {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
	})
}

func TestPromptDetectsDirtyNestedRepository(t *testing.T) {
	root := promptProject(t, promptPlan, map[string]string{".gitignore": "deps/module\n"})
	nested := filepath.Join(root, "deps", "module")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCycleDir(t, nested, "init", "-q")
	gitCycleDir(t, nested, "config", "user.name", "docflow test")
	gitCycleDir(t, nested, "config", "user.email", "test@example.invalid")
	writeCycleFile(t, nested, "tracked.txt", "initial\n")
	gitCycleDir(t, nested, "add", "-A")
	gitCycleDir(t, nested, "commit", "-q", "-m", "initial")
	writeCycleFile(t, nested, "tracked.txt", "changed\n")
	result, err := Prompt(root, "", func(string) ([]string, error) {
		return []string{"deps/module"}, nil
	})
	if err != nil || result.Code != 5 || !strings.Contains(result.Stderr, "deps/module/tracked.txt") {
		t.Fatalf("Prompt() = %#v, %v", result, err)
	}
}

func TestPromptReferenceErrorsDoNotChangeStatus(t *testing.T) {
	t.Run("missing read file", func(t *testing.T) {
		plan := strings.Replace(promptPlan, "Read:\n- a.py\nDo:\n- c", "Read:\n- later.py\nDo:\n- c", 1)
		root := promptProject(t, plan, nil)
		_, err := runPrompt(t, root, "")
		if err == nil || !strings.Contains(err.Error(), "docs/plans/P001-plan.md:") || !strings.Contains(err.Error(), "later.py: file not found") {
			t.Fatalf("Prompt() error = %v", err)
		}
		data, _ := os.ReadFile(filepath.Join(root, "docs", "plans", "P001-plan.md"))
		if !strings.Contains(string(data), "### [ ] P001.1.3") {
			t.Fatal("reference error changed unit status")
		}
	})
	t.Run("missing context anchor", func(t *testing.T) {
		plan := strings.Replace(promptPlan, "- docs/ctx.md", "- docs/ctx.md#nope", 1)
		root := promptProject(t, plan, nil)
		_, err := runPrompt(t, root, "")
		if err == nil || !strings.Contains(err.Error(), "docs/plans/P001-plan.md:12: error: docs/ctx.md: no anchor #nope") {
			t.Fatalf("Prompt() error = %v", err)
		}
	})
}

func TestPromptExplicitSelectionRules(t *testing.T) {
	t.Run("unfinished dependency", func(t *testing.T) {
		root := promptProject(t, promptPlan, nil)
		result, err := runPrompt(t, root, "P001.1.4")
		if err != nil || result.Code != 5 || !strings.Contains(result.Stderr, "P001.1.4 depends on unfinished P001.1.3") {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
	})
	t.Run("independent unit", func(t *testing.T) {
		plan := strings.Replace(promptPlan, "Commit: P001.1.4", "Depends: -\nCommit: P001.1.4", 1)
		root := promptProject(t, plan, nil)
		result, err := runPrompt(t, root, "P001.1.4")
		if err != nil || result.Code != 0 || !strings.Contains(result.Stdout, "### [~] P001.1.4 Fourth") || strings.Contains(result.Stdout, "## Handoff from Depends") {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
	})
	t.Run("other unit in progress", func(t *testing.T) {
		plan := strings.Replace(promptPlan, "Commit: P001.1.4", "Depends: -\nCommit: P001.1.4", 1)
		plan = strings.Replace(plan, "### [ ] P001.1.3", "### [~] P001.1.3", 1)
		root := promptProject(t, plan, nil)
		result, err := runPrompt(t, root, "P001.1.4")
		if err != nil || result.Code != 5 || !strings.Contains(result.Stderr, "P001.1.3 is in progress") {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
	})
	t.Run("done unit", func(t *testing.T) {
		root := promptProject(t, promptPlan, nil)
		result, err := runPrompt(t, root, "P001.1.1")
		if err != nil || result.Code != 5 || !strings.Contains(result.Stderr, "already [x]") {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
	})
	t.Run("unknown unit or plan", func(t *testing.T) {
		root := promptProject(t, promptPlan, nil)
		for _, id := range []string{"P001.1.9", "P009.1.1"} {
			if _, err := runPrompt(t, root, id); err == nil {
				t.Errorf("Prompt(%q) succeeded", id)
			}
		}
	})
	t.Run("draft plan", func(t *testing.T) {
		plan := strings.Replace(promptPlan, "status: active", "status: draft", 1)
		root := promptProject(t, plan, nil)
		_, err := runPrompt(t, root, "P001.1.3")
		if err == nil || !strings.Contains(err.Error(), "plan is a draft") {
			t.Fatalf("Prompt() error = %v", err)
		}
	})
	t.Run("blocked", func(t *testing.T) {
		plan := strings.Replace(promptPlan, "### [ ] P001.1.3", "### [!] P001.1.3", 1)
		root := promptProject(t, plan, nil)
		for _, id := range []string{"", "P001.1.3"} {
			result, err := runPrompt(t, root, id)
			if err != nil || result.Code != 3 {
				t.Errorf("Prompt(%q) = %#v, %v", id, result, err)
			}
		}
	})
	t.Run("nothing", func(t *testing.T) {
		plan := strings.ReplaceAll(promptPlan, "### [ ]", "### [x]")
		root := promptProject(t, plan, nil)
		result, err := runPrompt(t, root, "")
		if err != nil || result.Code != 1 || result.Stdout != "" || !strings.Contains(result.Stderr, "nothing to do") {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
	})
}

func writeMigrationProject(t *testing.T, version string, plans map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "plans"), 0o755); err != nil {
		t.Fatal(err)
	}
	text := "---\ndocflow: " + version + "\nread_budget: 40000\n---\n## Queue\n- P001\n\n## Backlog\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "docflow.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, plan := range plans {
		if err := os.WriteFile(filepath.Join(root, "docs", "plans", name), []byte(plan), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const migrationPlan = `---
id: P001
title: Plan
type: feature
status: active
created: 2026-10-24
executor: pi | zai/glm-5.3 | max
fallback: codex | gpt-6-luna | max; claude | claude-opus-5-5 | low; pi | glm-5.3 | high
replan: claude | claude-opus-5-5 | high
---
## Goal

Цель.

## Context

## Constraints

## Traps

## Decisions

## Phase 1: First

### [ ] P001.1.1 Unit
Read:
- a.txt
Do:
- work
Verify: ` + "`true`" + `
Commit: P001.1.1: unit
`

func TestMigrateTwoFoldsFallbackIntoReplan(t *testing.T) {
	root := writeMigrationProject(t, "2", map[string]string{
		"P001-plan.md":  migrationPlan,
		"P002-plain.md": strings.Replace(migrationPlan, "replan: claude | claude-opus-5-5 | high\n", "", 1),
	})
	result, err := Migrate(root)
	if err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	want := "migrated 2 -> 3\ndocs/plans/P001-plan.md: replan carries the fallback chain\n"
	if result.Code != clierrors.EXIT_OK || result.Stdout != want || result.Stderr != "" {
		t.Fatalf("Migrate() = (%d, %q, %q), want (%d, %q, \"\")", result.Code, result.Stdout, result.Stderr, clierrors.EXIT_OK, want)
	}
	text, _ := os.ReadFile(filepath.Join(root, "docs", "docflow.md"))
	if !strings.Contains(string(text), "docflow: 3") {
		t.Fatalf("docflow.md marker = %q", text)
	}
	plan, _ := os.ReadFile(filepath.Join(root, "docs", "plans", "P001-plan.md"))
	wantReplan := "replan: claude | claude-opus-5-5 | high; codex | gpt-6-luna | max; pi | glm-5.3 | high"
	found := ""
	for _, line := range strings.Split(string(plan), "\n") {
		if strings.HasPrefix(line, "replan:") {
			found = line
		}
	}
	if found != wantReplan {
		t.Fatalf("replan line = %q, want %q", found, wantReplan)
	}
	if !strings.Contains(string(plan), "fallback: codex | gpt-6-luna | max; claude | claude-opus-5-5 | low; pi | glm-5.3 | high") {
		t.Fatal("fallback chain changed")
	}
	plain, _ := os.ReadFile(filepath.Join(root, "docs", "plans", "P002-plain.md"))
	if strings.Contains(string(plain), "replan") {
		t.Fatal("plan without replan changed")
	}
}

func TestMigrateTwoSkipsArchiveAndPlansWithoutFallback(t *testing.T) {
	noFallback := strings.Replace(migrationPlan, "fallback: codex | gpt-6-luna | max; claude | claude-opus-5-5 | low; pi | glm-5.3 | high\n", "", 1)
	root := writeMigrationProject(t, "2", map[string]string{"P003-replan.md": noFallback})
	if err := os.MkdirAll(filepath.Join(root, "docs", "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "archive", "P000-old.md"), []byte(migrationPlan), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Migrate(root)
	if err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if result.Code != clierrors.EXIT_OK || result.Stdout != "migrated 2 -> 3\n" || result.Stderr != "" {
		t.Fatalf("Migrate() = (%d, %q, %q), want (%d, %q, \"\")", result.Code, result.Stdout, result.Stderr, clierrors.EXIT_OK, "migrated 2 -> 3\n")
	}
	plan, err := os.ReadFile(filepath.Join(root, "docs", "plans", "P003-replan.md"))
	if err != nil || string(plan) != noFallback {
		t.Fatalf("plan without fallback changed: %v", err)
	}
	archived, err := os.ReadFile(filepath.Join(root, "docs", "archive", "P000-old.md"))
	if err != nil || string(archived) != migrationPlan {
		t.Fatalf("archive plan changed: %v", err)
	}
}

func TestMigrateOneRunsThroughThree(t *testing.T) {
	root := writeMigrationProject(t, "1", map[string]string{"P001-plan.md": migrationPlan})
	result, err := Migrate(root)
	if err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	want := "migrated 1 -> 2\nmigrated 2 -> 3\ndocs/plans/P001-plan.md: replan carries the fallback chain\n"
	if result.Code != clierrors.EXIT_OK || result.Stdout != want {
		t.Fatalf("Migrate() = (%d, %q), want (%d, %q)", result.Code, result.Stdout, clierrors.EXIT_OK, want)
	}
	again, err := Migrate(root)
	if err != nil || again.Code != clierrors.EXIT_NOTHING || again.Stderr != "nothing to migrate\n" {
		t.Fatalf("repeat Migrate() = (%d, %q, %q), %v", again.Code, again.Stdout, again.Stderr, err)
	}
}
