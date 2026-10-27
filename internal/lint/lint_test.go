package lint

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
)

const testDocflow = `---
docflow: 3
read_budget: 40000
---
## Queue
- P001

## Backlog
- idea
`

const testPlan = `---
id: P001
title: Plan
type: feature
status: active
created: 2026-10-12
---
## Goal
Цель.

## Context

## Constraints

## Traps

## Decisions

## Phase 1: First

### [x] P001.1.1 First unit
Read:
- a.py
Do:
- b
Verify: ` + "`true`" + `
Traps: перенос
без отступа
Depends: -
Commit: P001.1.1: first unit
Handoff:
- c

## Phase 2: Second

### [ ] P001.2.1 Second unit
Read:
- a.py
Do:
- b
Verify: ` + "`true`" + `
Commit: P001.2.1: second unit
`

const testArchive = `---
id: P000
title: Old
closed: 2026-10-01
---
## Outcome
Есть.

## Decisions
- решение — почему — альтернатива

## Gotchas
- нет

## Follow-ups
- нет
`

const testPlanPath = "docs/plans/P001-plan.md"
const testArchivePath = "docs/archive/P000-old.md"

type projectOptions struct {
	docflow string
	plan    *string
	archive *string
	extra   map[string]string
}

func makeProject(t *testing.T, options projectOptions) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"docs/plans", "docs/archive"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if options.docflow == "" {
		options.docflow = testDocflow
	}
	writeProjectFile(t, root, "docs/docflow.md", options.docflow)
	writeProjectFile(t, root, "a.py", "print(1)\n")
	writeProjectFile(t, root, "a.txt", "a\n")
	if options.plan == nil {
		plan := testPlan
		options.plan = &plan
	}
	if options.plan != nil {
		writeProjectFile(t, root, testPlanPath, *options.plan)
	}
	if options.archive == nil {
		archive := testArchive
		options.archive = &archive
	}
	if options.archive != nil {
		writeProjectFile(t, root, testArchivePath, *options.archive)
	}
	for path, content := range options.extra {
		writeProjectFile(t, root, path, content)
	}
	return root
}

func writeProjectFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lintProject(t *testing.T, options projectOptions) (*Report, string) {
	t.Helper()
	root := makeProject(t, options)
	report, err := Lint(root)
	if err != nil {
		t.Fatalf("Lint() error = %v", err)
	}
	return report, root
}

func issue(t *testing.T, report *Report, text, level string, line int) Issue {
	t.Helper()
	for _, found := range report.Issues {
		if strings.Contains(found.Text, text) && found.Level == level && (line == 0 || found.Line == line) {
			return found
		}
	}
	t.Fatalf("no %s %q at line %d in %#v", level, text, line, report.Issues)
	return Issue{}
}

func assertClean(t *testing.T, report *Report) {
	t.Helper()
	if len(report.Issues) != 0 {
		t.Fatalf("issues = %#v, want none", report.Issues)
	}
}

func replaceOnce(t *testing.T, text, before, after string) string {
	t.Helper()
	if !strings.Contains(text, before) {
		t.Fatalf("fixture does not contain %q", before)
	}
	return strings.Replace(text, before, after, 1)
}

func TestLintValidProject(t *testing.T) {
	report, _ := lintProject(t, projectOptions{})
	assertClean(t, report)
}

func TestLintDocflow(t *testing.T) {
	t.Run("version", func(t *testing.T) {
		docflow := strings.Replace(testDocflow, "docflow: 3", "docflow: 0", 1)
		report, _ := lintProject(t, projectOptions{docflow: docflow})
		issue(t, report, "run docflow migrate", "error", 2)
	})
	t.Run("missing key", func(t *testing.T) {
		docflow := strings.Replace(testDocflow, "read_budget: 40000\n", "", 1)
		report, _ := lintProject(t, projectOptions{docflow: docflow})
		issue(t, report, "missing frontmatter key 'read_budget'", "error", 1)
	})
	t.Run("unknown key warning", func(t *testing.T) {
		docflow := strings.Replace(testDocflow, "---\n## Queue", "extra: 1\n---\n## Queue", 1)
		report, _ := lintProject(t, projectOptions{docflow: docflow})
		issue(t, report, "unknown frontmatter key 'extra'", "warning", 4)
		if len(report.Errors()) != 0 {
			t.Fatalf("errors = %#v", report.Errors())
		}
	})
	t.Run("sections", func(t *testing.T) {
		missing := strings.Replace(testDocflow, "## Backlog\n- idea\n", "", 1)
		report, _ := lintProject(t, projectOptions{docflow: missing})
		issue(t, report, "missing section 'Backlog'", "error", 1)
		swapped := strings.Replace(testDocflow, "## Queue\n- P001\n\n## Backlog\n- idea\n", "## Backlog\n- idea\n\n## Queue\n- P001\n", 1)
		report, _ = lintProject(t, projectOptions{docflow: swapped})
		issue(t, report, "sections out of order", "error", 5)
		report, _ = lintProject(t, projectOptions{docflow: testDocflow + "## Other\n"})
		issue(t, report, "unknown section 'Other'", "error", 10)
	})
	t.Run("queue ids", func(t *testing.T) {
		bad := strings.Replace(testDocflow, "- P001\n", "- P001\n- x1\n", 1)
		report, _ := lintProject(t, projectOptions{docflow: bad})
		issue(t, report, "bad plan id 'x1'", "error", 7)
		duplicate := strings.Replace(testDocflow, "- P001\n", "- P001\n- P001\n", 1)
		report, _ = lintProject(t, projectOptions{docflow: duplicate})
		issue(t, report, "duplicate P001 in Queue", "error", 7)
		missing := strings.Replace(testDocflow, "- P001\n", "- P001\n- P009\n", 1)
		report, _ = lintProject(t, projectOptions{docflow: missing})
		issue(t, report, "P009 in Queue has no plan", "error", 7)
	})
	t.Run("plan not queued", func(t *testing.T) {
		docflow := strings.Replace(testDocflow, "- P001\n", "", 1)
		report, _ := lintProject(t, projectOptions{docflow: docflow})
		issue(t, report, "plan P001 is not in Queue", "error", 2)
	})
	t.Run("parse error reported", func(t *testing.T) {
		bad := "## Goal\n"
		report, _ := lintProject(t, projectOptions{plan: &bad})
		found := issue(t, report, "missing frontmatter", "error", 1)
		if found.Path != testPlanPath {
			t.Fatalf("issue path = %q, want %q", found.Path, testPlanPath)
		}
	})
}

func TestLintPlanMetadataAndSections(t *testing.T) {
	tests := []struct {
		name, plan, finding string
		level               string
	}{
		{"missing key", strings.Replace(testPlan, "created: 2026-10-12\n", "", 1), "missing frontmatter key 'created'", "error"},
		{"empty key", strings.Replace(testPlan, "title: Plan", "title:", 1), "empty 'title'", "error"},
		{"bad id", strings.Replace(testPlan, "id: P001", "id: X1", 1), "bad plan id 'X1'", "error"},
		{"type", strings.Replace(testPlan, "type: feature", "type: chore", 1), "type 'chore' not in", "error"},
		{"status", strings.Replace(testPlan, "status: active", "status: done", 1), "status 'done' not in", "error"},
		{"missing section", strings.Replace(testPlan, "## Traps\n\n", "", 1), "missing section 'Traps'", "error"},
		{"unknown section", strings.Replace(testPlan, "## Decisions\n", "## Decisions\n\n## Notes\n", 1), "unknown section 'Notes'", "error"},
		{"section after phases", strings.Replace(testPlan, "## Decisions\n", "", 1) + "\n## Decisions\n", "section 'Decisions' after phases", "error"},
		{"unknown key warning", strings.Replace(testPlan, "title: Plan\n", "title: Plan\nextra: one\n", 1), "unknown frontmatter key 'extra'", "warning"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, _ := lintProject(t, projectOptions{plan: &test.plan})
			issue(t, report, test.finding, test.level, 0)
		})
	}
	t.Run("order", func(t *testing.T) {
		plan := strings.Replace(testPlan, "## Context\n\n## Constraints\n", "## Constraints\n\n## Context\n", 1)
		report, _ := lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "sections out of order", "error", 8)
	})
	t.Run("empty decisions allowed", func(t *testing.T) {
		report, _ := lintProject(t, projectOptions{})
		assertClean(t, report)
	})
}

func TestLintPermissionsReplanAndPrerequisites(t *testing.T) {
	withMeta := func(plan, meta string) string {
		return replaceOnce(t, plan, "created: 2026-10-12\n", "created: 2026-10-12\n"+meta)
	}
	execPlan := withMeta(testPlan, "executor: pi | x | -\nfallback: codex | gpt-5.5 | -\n")
	execPlan = replaceOnce(t, execPlan, "Verify: `true`\nCommit: P001.2.1", "Verify: `true`\nExecutor: pi | x | -\nCommit: P001.2.1")
	t.Run("permissions", func(t *testing.T) {
		for _, value := range []string{"full", "auto"} {
			plan := withMeta(testPlan, "permissions: "+value+"\n")
			report, _ := lintProject(t, projectOptions{plan: &plan})
			assertClean(t, report)
		}
		plan := withMeta(testPlan, "permissions: sudo\n")
		report, _ := lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "permissions 'sudo' not in: auto, full", "error", 7)
	})
	t.Run("replan", func(t *testing.T) {
		plan := withMeta(execPlan, "replan: codex | gpt-5.5 | -; claude | claude-opus-5-5 | high\n")
		report, _ := lintProject(t, projectOptions{plan: &plan})
		assertClean(t, report)
		plan = withMeta(testPlan, "replan: pi | x | -\n")
		report, _ = lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "replan needs executor mode", "error", 7)
		plan = withMeta(execPlan, "replan: claude\n")
		report, _ = lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "replan: expected \"agent | model | effort\"", "error", 0)
		plan = withMeta(execPlan, "replan: gpt | x | -\n")
		report, _ = lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "replan: unknown agent 'gpt'", "error", 0)
	})
	t.Run("prerequisites", func(t *testing.T) {
		prereq := "## Prerequisites\n- Аккаунт: `cat ~/logins/rmg.txt`\n- Симулятор запущен\n\n"
		base := strings.Replace(testPlan, "## Traps", prereq+"## Traps", 1)
		report, _ := lintProject(t, projectOptions{plan: &base})
		assertClean(t, report)
		once := strings.Replace(base, "- Аккаунт: `cat ~/logins/rmg.txt`", "- Аккаунт: `cat ~/logins/rmg.txt` (once)", 1)
		once = strings.Replace(once, "- Симулятор запущен", "- Симулятор запущен (once)", 1)
		report, _ = lintProject(t, projectOptions{plan: &once})
		assertClean(t, report)
		outside := strings.Replace(base, "## Trerequisites", "", 1)
		outside = strings.Replace(outside, "## Prerequisites\n", "## Prerequisites\ntext outside\n", 1)
		report, _ = lintProject(t, projectOptions{plan: &outside})
		issue(t, report, "Prerequisites: line outside a list item", "error", 0)
		empty := strings.Replace(base, "- Симулятор запущен", "- пустая: `` (once)", 1)
		report, _ = lintProject(t, projectOptions{plan: &empty})
		issue(t, report, "Prerequisites: empty command in backticks", "error", 0)
		before := strings.Replace(testPlan, "## Context\n", "## Prerequisites\n- раньше\n\n## Context\n", 1)
		report, _ = lintProject(t, projectOptions{plan: &before})
		issue(t, report, "sections out of order", "error", 0)
		late := testPlan + "\n## Prerequisites\n- late\n"
		report, _ = lintProject(t, projectOptions{plan: &late})
		issue(t, report, "section 'Prerequisites' after phases", "error", 0)
	})
}

func TestLintFallback(t *testing.T) {
	withMeta := func(plan, meta string) string {
		return replaceOnce(t, plan, "created: 2026-10-12\n", "created: 2026-10-12\n"+meta)
	}
	execPlan := withMeta(testPlan, "executor: claude | claude-opus-5-5 | high\n")
	t.Run("valid chain", func(t *testing.T) {
		plan := withMeta(execPlan, "fallback: codex | gpt-5.5 | -; pi | zai/glm-5.3 | high\n")
		report, _ := lintProject(t, projectOptions{plan: &plan})
		assertClean(t, report)
	})
	t.Run("executor mode required", func(t *testing.T) {
		plan := withMeta(testPlan, "fallback: codex | gpt-5.5 | -\n")
		report, _ := lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "fallback needs executor mode", "error", 0)
	})
	t.Run("triple required", func(t *testing.T) {
		plan := withMeta(execPlan, "fallback: codex | gpt-5.5\n")
		report, _ := lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "fallback: expected \"agent | model | effort\"", "error", 0)
	})
	t.Run("primary cannot repeat", func(t *testing.T) {
		plan := withMeta(execPlan, "fallback: claude | claude-opus-5-5 | high\n")
		report, _ := lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "fallback: repeats the executor assignment", "error", 0)
	})
	t.Run("semicolon splits triples", func(t *testing.T) {
		plan := withMeta(execPlan, "fallback: codex | model;version | -\n")
		report, _ := lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "fallback: expected \"agent | model | effort\"", "error", 0)
	})
}

func TestLintPlanTimeout(t *testing.T) {
	for _, value := range []string{"60", "86400"} {
		t.Run("valid-"+value, func(t *testing.T) {
			plan := strings.Replace(testPlan, "created: 2026-10-12\n", "created: 2026-10-12\ntimeout: "+value+"\n", 1)
			report, _ := lintProject(t, projectOptions{plan: &plan})
			assertClean(t, report)
		})
	}
	for _, value := range []string{"59", "86401", "60.5", "soon"} {
		t.Run("invalid-"+value, func(t *testing.T) {
			plan := strings.Replace(testPlan, "created: 2026-10-12\n", "created: 2026-10-12\ntimeout: "+value+"\n", 1)
			report, _ := lintProject(t, projectOptions{plan: &plan})
			issue(t, report, "timeout must be an integer from 60 to 86400 seconds", "error", 0)
		})
	}
	execPlan := strings.Replace(testPlan, "created: 2026-10-12\n", "created: 2026-10-12\nexecutor: pi | zai/glm-5.3 | high\nfallback: codex | gpt-6-luna | max\ntimeout: 60\n", 1)
	report, _ := lintProject(t, projectOptions{plan: &execPlan})
	assertClean(t, report)
}

func TestLintPhasesUnitsAndFields(t *testing.T) {
	tests := []struct{ name, plan, finding string }{
		{"bad phase heading", strings.Replace(testPlan, "## Phase 2: Second", "## Phase two", 1), "bad phase heading 'Phase two'"},
		{"bad unit heading", strings.Replace(testPlan, "### [ ] P001.2.1 Second unit", "### P001.2.1 Second unit", 1), "bad unit heading"},
		{"unknown unit status", strings.Replace(testPlan, "### [ ] P001.2.1", "### [?] P001.2.1", 1), "unknown status [?]"},
		{"wrong plan id prefix", strings.Replace(testPlan, "P001.2.1", "P002.2.1", 1), "unit id P002.2.1 must start with P001.2."},
		{"wrong phase id prefix", strings.Replace(testPlan, "P001.2.1", "P001.1.2", 1), "unit id P001.1.2 must start with P001.2."},
		{"field order", strings.Replace(testPlan, "Do:\n- b\nVerify: `true`\nCommit: P001.2.1", "Verify: `true`\nDo:\n- b\nCommit: P001.2.1", 1), "field 'Do' out of order"},
		{"model multi word", strings.Replace(testPlan, "Verify: `true`\nCommit: P001.2.1", "Verify: `true`\nModel: a b\nCommit: P001.2.1", 1), "Model must be one word"},
		{"model list", strings.Replace(testPlan, "Verify: `true`\nCommit: P001.2.1", "Verify: `true`\nModel:\n- fast\nCommit: P001.2.1", 1), "Model must be one word"},
		{"model after commit", strings.Replace(testPlan, "Commit: P001.2.1: second unit\n", "Commit: P001.2.1: second unit\nModel: fast\n", 1), "field 'Model' out of order"},
		{"unknown field", strings.Replace(testPlan, "Depends: -", "Needs: -", 1), "unknown field 'Needs'"},
		{"duplicate field", strings.Replace(testPlan, "Depends: -", "Depends: -\nDepends: -", 1), "duplicate field 'Depends'"},
		{"empty required field", strings.Replace(testPlan, "Do:\n- b\nVerify: `true`\nCommit: P001.2.1", "Do:\nVerify: `true`\nCommit: P001.2.1", 1), "unit P001.2.1: empty 'Do'"},
		{"verify syntax", strings.Replace(testPlan, "Verify: `true`\nCommit: P001.2.1", "Verify: check manually\nCommit: P001.2.1", 1), "Verify must be one command in backticks"},
		{"commit prefix", strings.Replace(testPlan, "Commit: P001.2.1: second unit", "Commit: second unit", 1), "Commit must start with \"P001.2.1: \""},
		{"commit empty", strings.Replace(testPlan, "Commit: P001.2.1: second unit", "Commit: P001.2.1: ", 1), "Commit must start with \"P001.2.1: \""},
		{"text outside fields warning", strings.Replace(testPlan, "Read:\n- a.py\nDo:\n- b\nVerify: `true`\nCommit: P001.2.1", "Read:\n- a.py\n\nпотерянный текст\nDo:\n- b\nVerify: `true`\nCommit: P001.2.1", 1), "unit P001.2.1: text outside fields"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, _ := lintProject(t, projectOptions{plan: &test.plan})
			level := "error"
			if strings.Contains(test.name, "warning") {
				level = "warning"
			}
			issue(t, report, test.finding, level, 0)
		})
	}
	t.Run("required fields", func(t *testing.T) {
		for _, item := range []struct{ key, old, replacement string }{
			{"Read", "Read:\n- a.py\n", ""},
			{"Do", "Do:\n- b\n", ""},
			{"Verify", "Verify: `true`\n", ""},
			{"Commit", "Commit: P001.2.1: second unit\n", ""},
		} {
			base := strings.Index(testPlan, "### [ ] P001.2.1")
			plan := testPlan[:base] + replaceOnce(t, testPlan[base:], item.old, item.replacement)
			report, _ := lintProject(t, projectOptions{plan: &plan})
			issue(t, report, "unit P001.2.1: missing '"+item.key+"'", "error", 0)
		}
	})
	t.Run("duplicate unit id", func(t *testing.T) {
		plan := testPlan + testPlan[strings.Index(testPlan, "### [ ] P001.2.1"):]
		report, _ := lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "duplicate unit id P001.2.1", "error", 0)
	})
	t.Run("unit outside phase", func(t *testing.T) {
		plan := strings.Replace(testPlan, "## Decisions\n", "## Decisions\n\n### [ ] P001.0.1 Stray\n", 1)
		report, _ := lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "unit P001.0.1 outside a phase", "error", 0)
	})
	t.Run("phase numbering", func(t *testing.T) {
		plan := strings.Replace(testPlan, "## Phase 2: Second", "## Phase 3: Second", 1)
		plan = strings.Replace(plan, "P001.2.1", "P001.3.1", 1)
		report, _ := lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "phase 3, expected 2", "error", 0)
	})
}

func TestLintArchiveAndIDs(t *testing.T) {
	t.Run("id shared with archive", func(t *testing.T) {
		archive := strings.Replace(testArchive, "id: P000", "id: P001", 1)
		report, _ := lintProject(t, projectOptions{archive: &archive})
		issue(t, report, "id P001 already used in docs/plans/P001-plan.md", "error", 2)
	})
	t.Run("duplicate plan id", func(t *testing.T) {
		root := makeProject(t, projectOptions{})
		writeProjectFile(t, root, "docs/plans/P001-copy.md", testPlan)
		report, err := Lint(root)
		if err != nil {
			t.Fatal(err)
		}
		issue(t, report, "id P001 already used", "error", 2)
	})
	tests := []struct {
		name, archive, finding string
		level                  string
	}{
		{"missing key", strings.Replace(testArchive, "closed: 2026-10-01\n", "", 1), "missing frontmatter key 'closed'", "error"},
		{"missing section", strings.Replace(testArchive, "## Gotchas\n- нет\n\n", "", 1), "missing section 'Gotchas'", "error"},
		{"empty section", strings.Replace(testArchive, "## Decisions\n- решение — почему — альтернатива\n", "## Decisions\n", 1), "empty section 'Decisions'", "error"},
		{"bad superseded_by", strings.Replace(testArchive, "closed: 2026-10-01\n", "closed: 2026-10-01\nsuperseded_by: foo\n", 1), "bad superseded_by 'foo'", "error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, _ := lintProject(t, projectOptions{archive: &test.archive})
			issue(t, report, test.finding, test.level, 0)
		})
	}
	t.Run("valid superseded_by", func(t *testing.T) {
		archive := strings.Replace(testArchive, "closed: 2026-10-01\n", "closed: 2026-10-01\nsuperseded_by: P002\n", 1)
		report, _ := lintProject(t, projectOptions{archive: &archive})
		assertClean(t, report)
	})
	t.Run("plan superseded_by is unknown warning", func(t *testing.T) {
		plan := strings.Replace(testPlan, "id: P001\n", "id: P001\nsuperseded_by: P002\n", 1)
		report, _ := lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "unknown frontmatter key 'superseded_by'", "warning", 3)
	})
	t.Run("comment in archive heading", func(t *testing.T) {
		archive := strings.Replace(testArchive, "## Outcome", "## Outcome           <!-- что теперь есть -->", 1)
		report, _ := lintProject(t, projectOptions{archive: &archive})
		assertClean(t, report)
	})
}

func TestLintDepends(t *testing.T) {
	withDepends := func(value string) string {
		return strings.Replace(testPlan, "Verify: `true`\nCommit: P001.2.1", "Verify: `true`\nDepends: "+value+"\nCommit: P001.2.1", 1)
	}
	t.Run("explicit valid", func(t *testing.T) {
		plan := withDepends("P001.1.1")
		report, _ := lintProject(t, projectOptions{plan: &plan})
		assertClean(t, report)
	})
	t.Run("other plan", func(t *testing.T) {
		plan := withDepends("P002.1.1")
		report, _ := lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "P002.1.1 in Depends is not a unit of this plan", "error", 42)
	})
	t.Run("bad id", func(t *testing.T) {
		plan := withDepends("first")
		report, _ := lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "bad unit id 'first' in Depends", "error", 42)
	})
	t.Run("self", func(t *testing.T) {
		plan := withDepends("P001.2.1")
		report, _ := lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "unit P001.2.1 depends on itself", "error", 42)
	})
	t.Run("forward dependency", func(t *testing.T) {
		plan := strings.Replace(testPlan, "Depends: -", "Depends: P001.2.1", 1)
		report, _ := lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "P001.2.1 in Depends comes after P001.1.1", "error", 29)
	})
	t.Run("list rejected", func(t *testing.T) {
		plan := strings.Replace(testPlan, "Verify: `true`\nCommit: P001.2.1", "Verify: `true`\nDepends:\n- P001.1.1\nCommit: P001.2.1", 1)
		report, _ := lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "Depends must be unit ids separated by commas", "error", 42)
	})
}

func TestLintReferencesAndReadBudget(t *testing.T) {
	readPlan := func(refs ...string) string {
		items := ""
		for _, ref := range refs {
			items += "- " + ref + "\n"
		}
		return strings.Replace(testPlan, "Read:\n- a.py\nDo:\n- b\nVerify: `true`\nCommit: P001.2.1", "Read:\n"+items+"Do:\n- b\nVerify: `true`\nCommit: P001.2.1", 1)
	}
	const doc = "# Title\n\n## Формат: общие правила\n\nТекст.\n\n```markdown\n## Goal\n```\n\n### Подраздел\n\n## Next section\n\n## Повтор\n\n## Повтор\nx\n"
	t.Run("missing file warns", func(t *testing.T) {
		plan := readPlan("a.py", "later.py")
		report, _ := lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "later.py: file not found", "warning", 39)
		if len(report.Errors()) > 0 {
			t.Fatalf("errors = %#v", report.Errors())
		}
	})
	t.Run("missing anchor errors", func(t *testing.T) {
		plan := readPlan("docs/d.md#nope")
		report, _ := lintProject(t, projectOptions{plan: &plan, extra: map[string]string{"docs/d.md": doc}})
		issue(t, report, "docs/d.md: no anchor #nope", "error", 38)
	})
	t.Run("context refs and prose hint", func(t *testing.T) {
		plan := strings.Replace(testPlan, "## Context\n", "## Context\n- docs/d.md#nope\n", 1)
		report, _ := lintProject(t, projectOptions{plan: &plan, extra: map[string]string{"docs/d.md": doc}})
		issue(t, report, "docs/d.md: no anchor #nope", "error", 12)
		plan = strings.Replace(testPlan, "## Context\n", "## Context\n- падает на пустом каталоге\n", 1)
		report, _ = lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "write repro as prose", "warning", 12)
	})
	t.Run("markdown links", func(t *testing.T) {
		plan := strings.Replace(testPlan, "## Context\n", "## Context\n- [d](../../docs/d.md)\n", 1)
		report, _ := lintProject(t, projectOptions{plan: &plan, extra: map[string]string{"docs/d.md": doc}})
		issue(t, report, "markdown link: write a bare path", "error", 12)
		plan = readPlan("`[a](../../a.py)`")
		report, _ = lintProject(t, projectOptions{plan: &plan})
		issue(t, report, "markdown link: write a bare path", "error", 38)
	})
	t.Run("read anchor has no context hint", func(t *testing.T) {
		plan := readPlan("docs/d.md#nope")
		report, _ := lintProject(t, projectOptions{plan: &plan, extra: map[string]string{"docs/d.md": doc}})
		issue(t, report, "docs/d.md: no anchor #nope", "error", 38)
		for _, finding := range report.Issues {
			if strings.Contains(finding.Text, "write repro") {
				t.Fatalf("unexpected context hint in read issue: %#v", finding)
			}
		}
	})
	t.Run("done read omitted", func(t *testing.T) {
		plan := strings.Replace(testPlan, "### [x] P001.1.1 First unit\nRead:\n- a.py", "### [x] P001.1.1 First unit\nRead:\n- gone.py", 1)
		report, _ := lintProject(t, projectOptions{plan: &plan})
		assertClean(t, report)
	})
	t.Run("read budget counts bytes", func(t *testing.T) {
		plan := readPlan("a.py", "big.txt")
		docflow := strings.Replace(testDocflow, "read_budget: 40000", "read_budget: 1000", 1)
		report, _ := lintProject(t, projectOptions{docflow: docflow, plan: &plan, extra: map[string]string{"big.txt": strings.Repeat("x", 4000)}})
		issue(t, report, "unit P001.2.1: ~", "warning", 36)
		issue(t, report, "tokens of context > read_budget 1000", "warning", 36)
		plan = readPlan("a.py")
		report, _ = lintProject(t, projectOptions{docflow: docflow, plan: &plan})
		assertClean(t, report)
	})
	t.Run("bad read budget", func(t *testing.T) {
		docflow := strings.Replace(testDocflow, "40000", "lots", 1)
		report, _ := lintProject(t, projectOptions{docflow: docflow})
		issue(t, report, "read_budget 'lots' must be a positive integer", "error", 3)
	})
}

func TestReadBudgetCountsPromptAndReadReferencesTogether(t *testing.T) {
	docflow := strings.Replace(testDocflow, "read_budget: 40000", "read_budget: 1", 1)
	report, root := lintProject(t, projectOptions{docflow: docflow})
	planText, err := format.ReadText(filepath.Join(root, filepath.FromSlash(testPlanPath)))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := format.ParsePlan(planText, testPlanPath)
	if err != nil {
		t.Fatal(err)
	}
	unit := plan.Unit("P001.2.1")
	prompt, err := cycle.PromptText(root, plan, unit, nil)
	if err != nil {
		t.Fatal(err)
	}
	tokens := (len([]byte(prompt)) + len("print(1)\n")) / 4
	issue(t, report, fmt.Sprintf("unit P001.2.1: ~%d tokens of context", tokens), "warning", 36)
}

func TestLintWholeReadLimit(t *testing.T) {
	readPlan := func(refs ...string) string {
		items := ""
		for _, ref := range refs {
			items += "- " + ref + "\n"
		}
		return strings.Replace(testPlan, "Read:\n- a.py\nDo:\n- b\nVerify: `true`\nCommit: P001.2.1", "Read:\n"+items+"Do:\n- b\nVerify: `true`\nCommit: P001.2.1", 1)
	}
	withLimit := func(limit string) string {
		if limit == "" {
			return testDocflow
		}
		return strings.Replace(testDocflow, "read_budget: 40000\n", "read_budget: 40000\nwhole_read_limit: "+limit+"\n", 1)
	}
	trapArchive := `---
id: P009
title: Trap
closed: 2026-10-02
---
## Outcome
Есть.

## Decisions
- решение — почему — альтернатива

## Gotchas
- ` + strings.Repeat("g", 400) + `

## Follow-ups
- нет
`
	big := strings.Repeat("x", 16000) + "\nsee docs/archive/P009-trap.md\n"
	bigTokens := len(big) / 4
	gotchaTokens := len(fmt.Sprintf("- (P009) %s", strings.Repeat("g", 400))) / 4
	want := fmt.Sprintf("big.txt is ~%d tokens whole (+%d tokens of archive gotchas); reference a range or #anchor", bigTokens, gotchaTokens)
	extra := map[string]string{
		"big.txt":                   big,
		"docs/archive/P009-trap.md": trapArchive,
		"docs/d.md":                 "# T\n\ntext\n",
	}
	t.Run("whole file over default limit warns with gotchas", func(t *testing.T) {
		plan := readPlan("a.py", "big.txt")
		report, _ := lintProject(t, projectOptions{plan: &plan, extra: extra})
		issue(t, report, want, "warning", 39)
		if len(report.Errors()) > 0 {
			t.Fatalf("errors = %#v", report.Errors())
		}
	})
	t.Run("range and anchor do not warn", func(t *testing.T) {
		plan := readPlan("big.txt:L2-3", "docs/d.md#t")
		report, _ := lintProject(t, projectOptions{plan: &plan, extra: extra})
		assertClean(t, report)
	})
	t.Run("done unit is not warned", func(t *testing.T) {
		plan := strings.Replace(testPlan, "### [x] P001.1.1 First unit\nRead:\n- a.py", "### [x] P001.1.1 First unit\nRead:\n- big.txt", 1)
		report, _ := lintProject(t, projectOptions{plan: &plan, extra: extra})
		assertClean(t, report)
	})
	t.Run("context warned when a unit is open", func(t *testing.T) {
		plan := strings.Replace(testPlan, "## Context\n", "## Context\n- big.txt\n", 1)
		report, _ := lintProject(t, projectOptions{plan: &plan, extra: extra})
		issue(t, report, want, "warning", 12)
	})
	t.Run("context silent when all units are closed", func(t *testing.T) {
		plan := strings.Replace(testPlan, "## Context\n", "## Context\n- big.txt\n", 1)
		plan = strings.Replace(plan, "### [ ] P001.2.1", "### [x] P001.2.1", 1)
		report, _ := lintProject(t, projectOptions{plan: &plan, extra: extra})
		assertClean(t, report)
	})
	t.Run("custom limit", func(t *testing.T) {
		plan := readPlan("big.txt")
		report, _ := lintProject(t, projectOptions{docflow: withLimit("5000"), plan: &plan, extra: extra})
		assertClean(t, report)
		report, _ = lintProject(t, projectOptions{docflow: withLimit("1000"), plan: &plan, extra: extra})
		issue(t, report, want, "warning", 38)
	})
	t.Run("bad value errors", func(t *testing.T) {
		report, _ := lintProject(t, projectOptions{docflow: withLimit("lots")})
		issue(t, report, "whole_read_limit 'lots' must be a positive integer", "error", 4)
	})
}

func TestLintStartBudget(t *testing.T) {
	withStart := func(read, budget string) string {
		return strings.Replace(testDocflow, "read_budget: 40000\n", "read_budget: 40000\nstart_read: "+read+"\nstart_budget: "+budget+"\n", 1)
	}
	t.Run("fits", func(t *testing.T) {
		docflow := withStart("a.py, big.txt", "100")
		report, _ := lintProject(t, projectOptions{docflow: docflow, extra: map[string]string{"big.txt": strings.Repeat("x", 100)}})
		assertClean(t, report)
	})
	t.Run("exceeded", func(t *testing.T) {
		docflow := withStart("a.py, big.txt", "100")
		report, _ := lintProject(t, projectOptions{docflow: docflow, extra: map[string]string{"big.txt": strings.Repeat("x", 4000)}})
		issue(t, report, "start_budget 100 (a.py: ~2, big.txt: ~1000)", "error", 4)
	})
	t.Run("missing file", func(t *testing.T) {
		docflow := withStart("nope.md", "100")
		report, _ := lintProject(t, projectOptions{docflow: docflow})
		issue(t, report, "nope.md: file not found", "error", 4)
	})
	t.Run("bad budget", func(t *testing.T) {
		docflow := withStart("a.py", "lots")
		report, _ := lintProject(t, projectOptions{docflow: docflow})
		issue(t, report, "start_budget 'lots' must be a positive integer", "error", 5)
	})
	t.Run("optional keys", func(t *testing.T) {
		report, _ := lintProject(t, projectOptions{})
		assertClean(t, report)
	})
}

func TestOutsideUsesPlatformPathSeparators(t *testing.T) {
	if !outside("nested/../outside") {
		t.Fatal("outside() = false for a parent path using slash")
	}
	backslashPath := `nested\..\outside`
	wantBackslash := filepath.Separator == '\\'
	if got := outside(backslashPath); got != wantBackslash {
		t.Fatalf("outside(%q) = %v, want %v for separator %q", backslashPath, got, wantBackslash, filepath.Separator)
	}
}

func TestLintCLIReportAndExitCode(t *testing.T) {
	root := makeProject(t, projectOptions{})
	var stdout bytes.Buffer
	code, err := CmdLint(root, &stdout)
	if err != nil || code != 0 || stdout.String() != "" {
		t.Fatalf("CmdLint() = (%d, %v, %q), want clean success", code, err, stdout.String())
	}
	plan := strings.Replace(testPlan, "type: feature", "type: unknown", 1)
	writeProjectFile(t, root, testPlanPath, plan)
	stdout.Reset()
	code, err = CmdLint(root, &stdout)
	if err != nil || code != 2 || !strings.Contains(stdout.String(), testPlanPath+":4: error:") {
		t.Fatalf("CmdLint() = (%d, %v, %q), want error report and exit 2", code, err, stdout.String())
	}
}

func TestCheckVersion(t *testing.T) {
	root := makeProject(t, projectOptions{})
	if err := CheckVersion(root); err != nil {
		t.Fatalf("CheckVersion() = %v", err)
	}
	docflow := strings.Replace(testDocflow, "docflow: 3", "docflow: 0", 1)
	writeProjectFile(t, root, "docs/docflow.md", docflow)
	err := CheckVersion(root)
	if err == nil || !strings.Contains(err.Error(), "docs/docflow.md:2: error: format version 0, expected 3; run docflow migrate") {
		t.Fatalf("CheckVersion() = %v, want current format error", err)
	}
	if DocflowVersion != "3" {
		t.Fatalf("DocflowVersion = %q, want 3", DocflowVersion)
	}
}

func TestParseExecutor(t *testing.T) {
	tests := []struct {
		value, agent, model, effort string
		err                         string
	}{
		{"claude | claude-opus-5-5 | high", "claude", "claude-opus-5-5", "high", ""},
		{"codex|gpt-5.5|-", "codex", "gpt-5.5", "", ""},
		{"claude | opus", "", "", "", "found 1"},
		{"claude | opus | high | x", "", "", "", "found 3"},
		{"claude", "", "", "", "found 0"},
		{" | opus | high", "", "", "", "empty agent"},
		{"claude |  | high", "", "", "", "empty model"},
		{"claude | opus | ", "", "", "", "empty effort"},
		{"claude | claude opus | high", "", "", "", "model 'claude opus'"},
		{"claude | opus | very high", "", "", "", "effort 'very high'"},
		{"gemini | pro | high", "", "", "", "unknown agent 'gemini'; known: claude, codex, pi"},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			agent, model, effort, err := ParseExecutor(test.value)
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("ParseExecutor() error = %v, want %q", err, test.err)
				}
				return
			}
			if err != nil || agent != test.agent || model != test.model {
				t.Fatalf("ParseExecutor() = (%q, %q, %v, %v)", agent, model, effort, err)
			}
			if test.effort == "" && effort != nil || test.effort != "" && (effort == nil || *effort != test.effort) {
				t.Fatalf("effort = %v, want %q", effort, test.effort)
			}
		})
	}
}

const executorPlan = `---
id: P001
title: Plan
type: feature
status: active
created: 2026-10-20
executor: claude | claude-opus-5-5 | high
fallback: codex | gpt-5.5 | -
---
## Goal
Цель.

## Context

## Constraints

## Traps

## Decisions

## Phase 1: First

### [x] P001.1.1 Done unit
Read:
- a.txt
Do:
- write a.txt
Verify: ` + "`test -f a.txt`" + `
Model: strong
Commit: P001.1.1: done unit
Handoff:
- готово

### [ ] P001.1.2 Inherits
Read:
- a.txt
Do:
- write b.txt
Verify: ` + "`test -f b.txt`" + `
Commit: P001.1.2: inherits

### [ ] P001.1.3 Overrides
Read:
- a.txt
Do:
- write c.txt
Verify: ` + "`test -f c.txt`" + `
Executor: codex | gpt-5.5 | -
Commit: P001.1.3: overrides
`

func parseExecutorPlan(t *testing.T, text string) *format.Plan {
	t.Helper()
	plan, err := format.ParsePlan(text, testPlanPath)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestUnitExecutorAndMode(t *testing.T) {
	plan := parseExecutorPlan(t, executorPlan)
	agent, model, effort, err := UnitExecutor(plan, plan.Units[1])
	if err != nil || agent != "claude" || model != "claude-opus-5-5" || effort == nil || *effort != "high" {
		t.Fatalf("inherited executor = (%q, %q, %v, %v)", agent, model, effort, err)
	}
	agent, model, effort, err = UnitExecutor(plan, plan.Units[2])
	if err != nil || agent != "codex" || model != "gpt-5.5" || effort != nil {
		t.Fatalf("override executor = (%q, %q, %v, %v)", agent, model, effort, err)
	}
	if !executorMode(plan) {
		t.Fatal("executorMode() = false for plan assignment")
	}
	legacy := strings.Replace(executorPlan, "executor: claude | claude-opus-5-5 | high\n", "", 1)
	legacy = strings.Replace(legacy, "fallback: codex | gpt-5.5 | -\n", "", 1)
	legacy = strings.Replace(legacy, "Executor: codex | gpt-5.5 | -\n", "", 1)
	plan = parseExecutorPlan(t, legacy)
	if executorMode(plan) || len(executorIssues(plan)) != 0 {
		t.Fatalf("legacy plan executor mode/issues = %v/%#v", executorMode(plan), executorIssues(plan))
	}
}

func TestLintExecutorAssignments(t *testing.T) {
	clean := func(plan string) *Report {
		t.Helper()
		report, _ := lintProject(t, projectOptions{plan: &plan})
		return report
	}
	find := func(t *testing.T, plan, text string) Issue {
		t.Helper()
		return issue(t, clean(plan), text, "error", 0)
	}
	t.Run("valid", func(t *testing.T) { assertClean(t, clean(executorPlan)) })
	t.Run("missing assignment", func(t *testing.T) {
		plan := strings.Replace(executorPlan, "executor: claude | claude-opus-5-5 | high\n", "", 1)
		found := find(t, plan, "unit P001.1.2: no executor")
		if found.Line != 33 {
			t.Fatalf("line = %d, want unit heading", found.Line)
		}
	})
	t.Run("model conflict", func(t *testing.T) {
		plan := strings.Replace(executorPlan, "Commit: P001.1.2", "Model: fast\nCommit: P001.1.2", 1)
		find(t, plan, "unit P001.1.2: Model: conflicts")
	})
	t.Run("done and skipped keep history", func(t *testing.T) {
		plan := strings.Replace(executorPlan, "[ ] P001.1.3", "[-] P001.1.3", 1)
		plan = strings.Replace(plan, "Executor: codex | gpt-5.5 | -\n", "Model: fast\n", 1)
		assertClean(t, clean(plan))
	})
	t.Run("bad head", func(t *testing.T) {
		plan := strings.Replace(executorPlan, "executor: claude | claude-opus-5-5 | high", "executor: claude | opus", 1)
		find(t, plan, "executor: expected \"agent | model | effort\" with 2 separators, found 1")
	})
	t.Run("bad unit", func(t *testing.T) {
		plan := strings.Replace(executorPlan, "Executor: codex | gpt-5.5 | -", "Executor: robot | x | high", 1)
		find(t, plan, "unit P001.1.3: Executor: unknown agent 'robot'")
	})
	t.Run("multiline assignment", func(t *testing.T) {
		plan := strings.Replace(executorPlan, "Executor: codex | gpt-5.5 | -", "Executor: codex | gpt-5.5\n| high", 1)
		find(t, plan, "unit P001.1.3: Executor must be one line")
		plan = strings.Replace(executorPlan, "Executor: codex | gpt-5.5 | -", "Executor:\n- codex | gpt-5.5 | -", 1)
		find(t, plan, "unit P001.1.3: Executor must be one line")
	})
	t.Run("multiline frontmatter parse error", func(t *testing.T) {
		plan := strings.Replace(executorPlan, "executor: claude | claude-opus-5-5 | high", "executor: claude | opus\n  | high", 1)
		report := clean(plan)
		issue(t, report, "bad frontmatter line: '  | high'", "error", 8)
	})
	t.Run("duplicate field and frontmatter", func(t *testing.T) {
		plan := strings.Replace(executorPlan, "Executor: codex | gpt-5.5 | -\n", "Executor: codex | gpt-5.5 | -\nExecutor: codex | gpt-5.5 | -\n", 1)
		find(t, plan, "unit P001.1.3: duplicate field 'Executor'")
		plan = strings.Replace(executorPlan, "executor: claude | claude-opus-5-5 | high\n", "executor: claude | claude-opus-5-5 | high\nexecutor: claude | claude-opus-5-5 | high\n", 1)
		issue(t, clean(plan), "duplicate frontmatter key 'executor'", "error", 8)
	})
	t.Run("invalid duplicate is validated", func(t *testing.T) {
		plan := strings.Replace(executorPlan, "Executor: codex | gpt-5.5 | -\n", "Executor: codex | gpt-5.5 | -\nExecutor: robot | x | high\n", 1)
		report := clean(plan)
		issue(t, report, "unit P001.1.3: duplicate field 'Executor'", "error", 0)
		issue(t, report, "unit P001.1.3: Executor: unknown agent 'robot'", "error", 0)
	})
	t.Run("field order", func(t *testing.T) {
		plan := strings.Replace(executorPlan, "Executor: codex | gpt-5.5 | -\n", "", 1)
		plan = strings.Replace(plan, "Commit: P001.1.3: overrides\n", "Commit: P001.1.3: overrides\nExecutor: codex | gpt-5.5 | -\n", 1)
		find(t, plan, "field 'Executor' out of order")
	})
}

func TestPlanPermissionsAndPrerequisitesHelpers(t *testing.T) {
	planText := strings.Replace(testPlan, "created: 2026-10-12\n", "created: 2026-10-12\npermissions: full\n", 1)
	planText = strings.Replace(planText, "## Traps", "## Prerequisites\n- Аккаунт: `cat ~/logins/rmg.txt`\n- Симулятор запущен\n\n## Traps", 1)
	plan := parseExecutorPlan(t, planText)
	if got := PlanPermissions(plan); got != "full" {
		t.Fatalf("PlanPermissions() = %q, want full", got)
	}
	prerequisites := PlanPrerequisites(plan)
	if len(prerequisites) != 2 || prerequisites[0].Text != "Аккаунт" || prerequisites[0].Command == nil || *prerequisites[0].Command != "cat ~/logins/rmg.txt" || prerequisites[1].Text != "Симулятор запущен" || prerequisites[1].Command != nil {
		t.Fatalf("PlanPrerequisites() = %#v", prerequisites)
	}
}

func TestLintRoles(t *testing.T) {
	clean := func(plan string) *Report {
		t.Helper()
		report, _ := lintProject(t, projectOptions{plan: &plan})
		return report
	}
	withMeta := func(meta string) string {
		return replaceOnce(t, executorPlan, "fallback: codex | gpt-5.5 | -\n", "fallback: codex | gpt-5.5 | -\n"+meta)
	}
	// withRole moves unit P001.1.2 (no assignment of its own) onto a role.
	withRole := func(plan, role string) string {
		return replaceOnce(t, plan, "Commit: P001.1.2: inherits", "Role: "+role+"\nCommit: P001.1.2: inherits")
	}
	t.Run("valid role chain and field", func(t *testing.T) {
		plan := withRole(withMeta("heavy: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high\n"), "heavy")
		assertClean(t, clean(plan))
	})
	t.Run("plan without roles stays as in format 2", func(t *testing.T) {
		assertClean(t, clean(executorPlan))
	})
	t.Run("chain segments must parse", func(t *testing.T) {
		plan := withRole(withMeta("heavy: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol\n"), "heavy")
		issue(t, clean(plan), "heavy: expected \"agent | model | effort\"", "error", 0)
	})
	t.Run("chain repeats agent and model", func(t *testing.T) {
		plan := withRole(withMeta("heavy: pi | zai/glm-5.3 | max; pi | zai/glm-5.3 | high\n"), "heavy")
		issue(t, clean(plan), "heavy: route 'pi | zai/glm-5.3' repeats in the chain", "error", 0)
	})
	t.Run("single-route chain warns", func(t *testing.T) {
		plan := withRole(withMeta("heavy: pi | zai/glm-5.3 | max\n"), "heavy")
		issue(t, clean(plan), "no fallback for heavy", "warning", 0)
	})
	t.Run("role key without units warns", func(t *testing.T) {
		plan := withMeta("heavy: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high\n")
		issue(t, clean(plan), "no units with role heavy", "warning", 0)
	})
	t.Run("unknown role value", func(t *testing.T) {
		plan := withRole(executorPlan, "executor")
		issue(t, clean(plan), "unit P001.1.2: Role must be one of: cheap, heavy, review, acceptance", "error", 0)
	})
	t.Run("role conflicts with executor", func(t *testing.T) {
		plan := withMeta("heavy: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high\n")
		plan = replaceOnce(t, plan, "Commit: P001.1.3: overrides", "Role: heavy\nCommit: P001.1.3: overrides")
		issue(t, clean(plan), "unit P001.1.3: Role: conflicts with Executor:", "error", 0)
	})
	t.Run("role without a chain", func(t *testing.T) {
		plan := withRole(executorPlan, "heavy")
		issue(t, clean(plan), "unit P001.1.2: Role 'heavy' has no 'heavy:' chain in the frontmatter", "error", 0)
	})
	t.Run("role must be one line", func(t *testing.T) {
		plan := withMeta("heavy: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high\n")
		plan = replaceOnce(t, plan, "Commit: P001.1.2: inherits", "Role:\n- heavy\nCommit: P001.1.2: inherits")
		issue(t, clean(plan), "unit P001.1.2: Role must be one line", "error", 0)
	})
	t.Run("role key needs executor mode", func(t *testing.T) {
		plan := replaceOnce(t, testPlan, "created: 2026-10-12\n", "created: 2026-10-12\nheavy: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high\n")
		issue(t, clean(plan), "heavy needs executor mode", "error", 0)
	})
	t.Run("role field needs executor mode", func(t *testing.T) {
		plan := replaceOnce(t, testPlan, "Commit: P001.2.1: second unit", "Role: heavy\nCommit: P001.2.1: second unit")
		issue(t, clean(plan), "unit P001.2.1: Role needs executor mode", "error", 0)
	})
	t.Run("field order with Role", func(t *testing.T) {
		plan := withRole(withMeta("heavy: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high\n"), "heavy")
		plan = replaceOnce(t, plan, "Role: heavy\nCommit: P001.1.2", "Commit: P001.1.2")
		plan = replaceOnce(t, plan, "Commit: P001.1.2: inherits", "Commit: P001.1.2: inherits\nRole: heavy")
		issue(t, clean(plan), "field 'Role' out of order", "error", 0)
	})
	t.Run("replan chain", func(t *testing.T) {
		plan := withMeta("replan: codex | gpt-5.5 | -\n")
		issue(t, clean(plan), "no fallback for replan", "warning", 0)
		plan = withMeta("replan: codex | gpt-5.5 | -; codex | gpt-5.5 | high\n")
		issue(t, clean(plan), "replan: route 'codex | gpt-5.5' repeats in the chain", "error", 0)
		plan = withMeta("replan: codex | gpt-5.5 | -; claude | claude-opus-5-5 | high\n")
		assertClean(t, clean(plan))
	})
	t.Run("executor without fallback warns", func(t *testing.T) {
		plan := strings.Replace(executorPlan, "fallback: codex | gpt-5.5 | -\n", "", 1)
		report := clean(plan)
		issue(t, report, "no fallback for executor", "warning", 7)
		issue(t, report, "unit P001.1.3: no fallback for executor", "warning", 0)
	})
}
