package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/lint"
)

func TestVersionStringOutsideGitRepository(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "docflow")
	if got := versionString(executable); got != version {
		t.Fatalf("versionString() = %q, want %q", got, version)
	}
}

func TestVersionStringInGitRepository(t *testing.T) {
	repo := t.TempDir()
	binDir := filepath.Join(repo, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(binDir, "docflow")
	if err := os.WriteFile(executable, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, repo, "init", "-q")
	gitTestCommand(t, repo, "config", "user.email", "docflow-test@example.invalid")
	gitTestCommand(t, repo, "config", "user.name", "docflow test")
	gitTestCommand(t, repo, "add", "bin/docflow")
	gitTestCommand(t, repo, "commit", "-qm", "initial")

	sha := strings.TrimSpace(gitTestCommand(t, repo, "rev-parse", "--short", "HEAD"))
	want := version + " (" + sha + ")"
	if got := versionString(executable); got != want {
		t.Fatalf("versionString() = %q, want %q", got, want)
	}

	link := filepath.Join(t.TempDir(), "docflow-link")
	if err := os.Symlink(executable, link); err != nil {
		t.Fatal(err)
	}
	if got := versionString(link); got != want {
		t.Fatalf("versionString() for symlink = %q, want %q", got, want)
	}
}

func TestVersionFlagAliases(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := versionString(executable) + "\n"
	for _, flag := range []string{"--version", "-V"} {
		t.Run(flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			parsed, code := parseArgs([]string{flag}, &stdout, &stderr)
			if parsed != nil || code != 0 {
				t.Fatalf("parseArgs() = (%v, %d), want (nil, 0)", parsed, code)
			}
			if got := stdout.String(); got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
			if stderr.Len() != 0 {
				t.Fatalf("version wrote to stderr: %q", stderr.String())
			}
		})
	}
}

func TestParseArgsHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	parsed, code := parseArgs([]string{"--help"}, &stdout, &stderr)
	if parsed != nil || code != 0 {
		t.Fatalf("parseArgs() = (%v, %d), want (nil, 0)", parsed, code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("help wrote to stderr: %q", stderr.String())
	}
	if stdout.String() != rootHelp {
		t.Fatalf("help output differs from the Python CLI contract\n%s", stdout.String())
	}
}

func TestParseArgsUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	parsed, code := parseArgs([]string{"nonsense"}, &stdout, &stderr)
	if parsed != nil || code != 2 {
		t.Fatalf("parseArgs() = (%v, %d), want (nil, 2)", parsed, code)
	}
	want := "usage: docflow [-h] [--version] command ...\n" +
		"docflow: error: argument command: invalid choice: 'nonsense' (choose from " + commandNames() + ")\n"
	if got := stderr.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unknown command wrote to stdout: %q", stdout.String())
	}
}

func TestParseArgsInvalidInteger(t *testing.T) {
	var stdout, stderr bytes.Buffer
	parsed, code := parseArgs([]string{"run", "--attempts", "nope"}, &stdout, &stderr)
	if parsed != nil || code != 2 {
		t.Fatalf("parseArgs() = (%v, %d), want (nil, 2)", parsed, code)
	}
	want := "usage: docflow run [-h] [--config PATH] [--agent NAME] [--model M]\n" +
		"                   [--effort E] [--attempts N] [--replans N] [--max-units N]\n" +
		"                   [--no-wait] [--no-notify] [--dry-run] [--all] [--plan ID]\n" +
		"                   [--print-config] [--yes] [-v] [--stop]\n" +
		"docflow run: error: argument --attempts: invalid int value: 'nope'\n"
	if got := stderr.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestRunnerOnlyCommandRefusesInRunner(t *testing.T) {
	t.Setenv(runnerEnv, "P024.1.1")
	var stdout, stderr bytes.Buffer
	code := run([]string{"commit", "P024.1.1"}, &stdout, &stderr)
	if code != 5 {
		t.Fatalf("run() = %d, want 5", code)
	}
	want := "commit: the runner drives P024.1.1 and runs it itself; end your reply with the result JSON\n"
	if got := stderr.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestLintCommandExitCodesAndVersionPrecheck(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"docs/plans", "docs/archive"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	docflow := "---\ndocflow: 3\nread_budget: 40000\n---\n## Queue\n- P001\n\n## Backlog\n"
	plan := "---\nid: P001\ntitle: Plan\ntype: feature\nstatus: active\ncreated: 2026-10-22\n---\n" +
		"## Goal\nGoal.\n\n## Context\n\n## Constraints\n\n## Traps\n\n## Decisions\n\n## Phase 1: First\n\n" +
		"### [ ] P001.1.1 Unit\nRead:\n- fixture.txt\nDo:\n- edit fixture\nVerify: `true`\nCommit: P001.1.1: update fixture\n"
	for name, content := range map[string]string{
		"docs/docflow.md":         docflow,
		"docs/plans/P001-plan.md": plan,
		"fixture.txt":             "fixture\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	runLint := func() (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"lint"}, &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
	if code, stdout, stderr := runLint(); code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("clean lint = (%d, %q, %q)", code, stdout, stderr)
	}
	plan = strings.Replace(plan, "title: Plan\n", "title: Plan\nextra: 1\n", 1)
	if err := os.WriteFile(filepath.Join(root, "docs/plans/P001-plan.md"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := runLint(); code != 0 || !strings.Contains(stdout, "docs/plans/P001-plan.md:4: warning:") || stderr != "" {
		t.Fatalf("warning lint = (%d, %q, %q)", code, stdout, stderr)
	}
	plan = strings.Replace(plan, "type: feature", "type: invalid", 1)
	if err := os.WriteFile(filepath.Join(root, "docs/plans/P001-plan.md"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := runLint(); code != 2 || !strings.Contains(stdout, "docs/plans/P001-plan.md:5: error:") || stderr != "" {
		t.Fatalf("error lint = (%d, %q, %q)", code, stdout, stderr)
	}
	docflow = strings.Replace(docflow, "docflow: 3", "docflow: 0", 1)
	if err := os.WriteFile(filepath.Join(root, "docs/docflow.md"), []byte(docflow), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := runLint(); code != 2 || stdout != "" || !strings.Contains(stderr, "docs/docflow.md:2: error: format version 0, expected 3; run docflow migrate") {
		t.Fatalf("old format lint = (%d, %q, %q)", code, stdout, stderr)
	}
}

func TestEveryCommandHasHandler(t *testing.T) {
	for _, spec := range commands {
		if handlers[spec.name] == nil {
			t.Errorf("command %q has no handler", spec.name)
		}
	}
}

func TestBlockNeedsDefaultsToHuman(t *testing.T) {
	parsed, code := parseArgs([]string{"block", "P001.1.2", "--reason", "нужен вход"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 || parsed == nil || parsed.values["--needs"] != "human" {
		t.Fatalf("block defaults = %#v, %d; want needs=human", parsed, code)
	}
}

func TestUnblockNoteIsOptional(t *testing.T) {
	parsed, code := parseArgs([]string{"unblock", "P001.1.2"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 || parsed == nil || parsed.values["id"] != "P001.1.2" {
		t.Fatalf("unblock without --note = %#v, %d", parsed, code)
	}
	if note, _ := parsed.values["--note"].(string); note != "" {
		t.Fatalf("unblock default note = %#v, want empty", parsed.values["--note"])
	}
	parsed, code = parseArgs([]string{"unblock", "P001.1.2", "--note", "вошёл в аккаунт"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 || parsed == nil || parsed.values["--note"] != "вошёл в аккаунт" {
		t.Fatalf("unblock with --note = %#v, %d", parsed, code)
	}
}

func TestUnblockRefusesInRunner(t *testing.T) {
	t.Setenv(runnerEnv, "P041.1.1")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"unblock", "P041.1.1", "--note", "готово"}, &stdout, &stderr); code != 5 {
		t.Fatalf("run() = %d, want 5", code)
	}
	if !strings.Contains(stderr.String(), "the runner drives P041.1.1") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestPreflightAndGateKeysLintClean(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"docs/plans", "docs/archive"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	docflow := "---\ndocflow: 3\nread_budget: 40000\npreflight: test -f ready\n---\n## Queue\n- P001\n\n## Backlog\n"
	plan := "---\nid: P001\ntitle: Plan\ntype: feature\nstatus: active\ncreated: 2026-10-22\ngate: test -f ready\n---\n" +
		"## Goal\nGoal.\n\n## Context\n\n## Constraints\n\n## Prerequisites\n\n## Traps\n\n## Decisions\n\n## Phase 1: First\n\n" +
		"### [~] P001.1.1 Unit\nRead:\n- fixture.txt\nDo:\n- edit fixture\nVerify: `true`\nCommit: P001.1.1: unit\n"
	for name, content := range map[string]string{
		"docs/docflow.md":         docflow,
		"docs/plans/P001-plan.md": plan,
		"fixture.txt":             "fixture\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	report, err := lint.Lint(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Issues) != 0 {
		t.Fatalf("lint findings with preflight/gate = %#v", report.Issues)
	}
}

func TestVerifyFindsProjectRootFromSubdirectory(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"docs/plans", "docs/archive", "src"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	docflow := "---\ndocflow: 3\nread_budget: 40000\n---\n## Queue\n- P001\n\n## Backlog\n"
	plan := "---\nid: P001\ntitle: Plan\ntype: feature\nstatus: active\ncreated: 2026-10-22\n---\n" +
		"## Goal\nGoal.\n\n## Context\n\n## Constraints\n\n## Prerequisites\n\n## Traps\n\n## Decisions\n\n## Phase 1: First\n\n" +
		"### [~] P001.1.1 Unit\nRead:\n- a.py\nDo:\n- edit fixture\nVerify: `test -f a.py && echo checked`\nCommit: P001.1.1: unit\n"
	for name, content := range map[string]string{
		"docs/docflow.md":         docflow,
		"docs/plans/P001-plan.md": plan,
		"a.py":                    "fixture\n",
	} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitTestCommand(t, root, "init", "-q")
	gitTestCommand(t, root, "config", "user.email", "docflow-test@example.invalid")
	gitTestCommand(t, root, "config", "user.name", "docflow test")
	gitTestCommand(t, root, "add", "-A")
	gitTestCommand(t, root, "commit", "-qm", "initial")
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(root, "src")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	var stdout, stderr bytes.Buffer
	if code := run([]string{"verify", "P001.1.1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("verify from subdirectory = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got, want := stdout.String(), "P001.1.1: verify passed: test -f a.py && echo checked\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestCommandsMatchDesign(t *testing.T) {
	want := []string{"init", "status", "next", "prompt", "verify", "done", "commit", "finish", "stats", "block", "unblock", "lint", "new", "activate", "close", "freeze", "thaw", "note", "migrate", "install-skills", "doctor", "roster", "run", "install", "update", "uninstall"}
	got := make([]string, len(commands))
	for i, spec := range commands {
		got[i] = spec.name
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %#v, want %#v", got, want)
	}
}

func TestSkillAgentDefaultsAndChoices(t *testing.T) {
	for _, name := range []string{"init", "install-skills", "doctor"} {
		t.Run(name, func(t *testing.T) {
			parsed, code := parseArgs([]string{name}, &bytes.Buffer{}, &bytes.Buffer{})
			if code != 0 || parsed == nil || parsed.values["--agent"] != "claude" {
				t.Fatalf("default args = %#v, %d", parsed, code)
			}
			parsed, code = parseArgs([]string{name, "--agent", "codex"}, &bytes.Buffer{}, &bytes.Buffer{})
			if code != 0 || parsed == nil || parsed.values["--agent"] != "codex" {
				t.Fatalf("codex args = %#v, %d", parsed, code)
			}
			var stderr bytes.Buffer
			parsed, code = parseArgs([]string{name, "--agent", "other"}, &bytes.Buffer{}, &stderr)
			if code != clierrors.EXIT_FORMAT || parsed != nil {
				t.Fatalf("invalid agent args = %#v, %d", parsed, code)
			}
		})
	}
}

func gitTestCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

// rosterMinimal is the smallest valid roster for the CLI tests.
const rosterMinimal = `spend = ["zai", "codex"]
[role]
cheap = "cheap"
executor = "standard"
heavy = "strong"
review = "strong"
acceptance = "trusted"
replan = "trusted"
[route.z1]
run = "pi | zai/m | high"
tier = "cheap"
[route.zs]
run = "pi | zai/m2 | high"
tier = "strong"
[route.c1]
run = "codex | c | high"
tier = "cheap"
[route.cs]
run = "codex | c2 | high"
tier = "strong"
`

func writeRosterFile(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "roster.toml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRosterShowCommand(t *testing.T) {
	path := writeRosterFile(t, rosterMinimal)
	var stdout, stderr bytes.Buffer
	parsed, code := parseArgs([]string{"roster", "show", "--roster", path}, &stdout, &stderr)
	if parsed == nil || code != 0 {
		t.Fatalf("parseArgs() = (%v, %d), want (parsed, 0)", parsed, code)
	}
	err := handleRoster(parsed)
	result, ok := err.(commandResult)
	if !ok {
		t.Fatalf("handleRoster() error = %v, want commandResult", err)
	}
	if result.code != 0 {
		t.Fatalf("handleRoster() code = %d, stdout %q", result.code, result.stdout)
	}
	want := `spend: zai, codex · check: - · trusted: -
executor: pi | zai/m2 | high
fallback: codex | c2 | high
cheap: pi | zai/m | high; codex | c | high
heavy: pi | zai/m2 | high; codex | c2 | high
review: pi | zai/m2 | high; codex | c2 | high
acceptance: pi | zai/m2 | high; codex | c2 | high
replan: pi | zai/m2 | high; codex | c2 | high
`
	if result.stdout != want {
		t.Fatalf("roster show stdout =\n%s\nwant\n%s", result.stdout, want)
	}
}

func TestRosterShowProfile(t *testing.T) {
	path := writeRosterFile(t, rosterMinimal+"\n[profile.reversed]\nspend = [\"codex\", \"zai\"]\n")
	parsed, code := parseArgs([]string{"roster", "show", "--profile", "reversed", "--roster", path}, &bytes.Buffer{}, &bytes.Buffer{})
	if parsed == nil || code != 0 {
		t.Fatalf("parseArgs() = (%v, %d), want (parsed, 0)", parsed, code)
	}
	err := handleRoster(parsed)
	result, ok := err.(commandResult)
	if !ok || result.code != 0 {
		t.Fatalf("handleRoster() = %#v, error %v", result, err)
	}
	lines := strings.Split(strings.TrimRight(result.stdout, "\n"), "\n")
	if lines[0] != "spend: codex, zai · check: - · trusted: -" {
		t.Fatalf("spend line = %q", lines[0])
	}
	if lines[1] != "executor: codex | c2 | high" || lines[2] != "fallback: pi | zai/m2 | high" {
		t.Fatalf("executor lines = %q, %q", lines[1], lines[2])
	}
}

func TestRosterShowErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "absent.toml")
		parsed, code := parseArgs([]string{"roster", "show", "--roster", path}, &bytes.Buffer{}, &bytes.Buffer{})
		if parsed == nil || code != 0 {
			t.Fatalf("parseArgs() = (%v, %d)", parsed, code)
		}
		err := handleRoster(parsed)
		if !strings.Contains(err.Error(), "absent.toml") {
			t.Fatalf("error = %v, want it to name the path", err)
		}
		if coded, ok := err.(interface{ ExitCode() int }); !ok || coded.ExitCode() != clierrors.EXIT_FORMAT {
			t.Fatalf("exit code of %v is not %d", err, clierrors.EXIT_FORMAT)
		}
	})
	t.Run("unknown subcommand", func(t *testing.T) {
		parsed, code := parseArgs([]string{"roster", "bogus"}, &bytes.Buffer{}, &bytes.Buffer{})
		if parsed == nil || code != 0 {
			t.Fatalf("parseArgs() = (%v, %d)", parsed, code)
		}
		err := handleRoster(parsed)
		if !strings.Contains(err.Error(), "unknown roster subcommand 'bogus'") {
			t.Fatalf("error = %v, want unknown roster subcommand", err)
		}
	})
	t.Run("apply without plan id", func(t *testing.T) {
		parsed, code := parseArgs([]string{"roster", "apply"}, &bytes.Buffer{}, &bytes.Buffer{})
		if parsed == nil || code != 0 {
			t.Fatalf("parseArgs() = (%v, %d)", parsed, code)
		}
		err := handleRoster(parsed)
		if !strings.Contains(err.Error(), "no plan id") {
			t.Fatalf("error = %v, want no plan id", err)
		}
	})
	t.Run("unknown profile", func(t *testing.T) {
		path := writeRosterFile(t, rosterMinimal)
		parsed, _ := parseArgs([]string{"roster", "show", "--profile", "nope", "--roster", path}, &bytes.Buffer{}, &bytes.Buffer{})
		err := handleRoster(parsed)
		if !strings.Contains(err.Error(), "unknown profile 'nope'") {
			t.Fatalf("error = %v, want unknown profile", err)
		}
	})
}

// applyTestPlan is a plan with a Role unit for the apply smoke test.
func applyTestPlan() string {
	return `---
id: P040
title: Roster apply
type: feature
status: active
created: 2026-10-12
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

## Phase 1: First

### [ ] P040.1.1 First unit
Read:
- a.py
Do:
- work
Verify: ` + "`true`" + `
Role: heavy
Commit: P040.1.1: first
`
}

func TestRosterApplyCommand(t *testing.T) {
	root := makeLifecycleProject(t, []string{"P040"}, nil, map[string]string{"P040": applyTestPlan()}, false)
	initLifecycleGit(t, root)
	path := writeRosterFile(t, rosterMinimal)
	code, stdout, stderr := callLifecycleCLI(t, root, "roster", "apply", "P040", "--roster", path)
	if code != 0 {
		t.Fatalf("roster apply code = %d, stdout:\n%sstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "commit: P040: apply roster") {
		t.Fatalf("roster apply stdout =\n%s\nwant the commit hint", stdout)
	}
	code, stdout, _ = callLifecycleCLI(t, root, "roster", "apply", "P040", "--roster", path)
	if code != 1 {
		t.Fatalf("second roster apply code = %d, want 1, stdout:\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "header already matches the roster") {
		t.Fatalf("second roster apply stdout =\n%s\nwant the no-change note", stdout)
	}
}

func TestRosterHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	parsed, code := parseArgs([]string{"roster", "--help"}, &stdout, &stderr)
	if parsed != nil || code != 0 {
		t.Fatalf("parseArgs() = (%v, %d), want (nil, 0)", parsed, code)
	}
	if stdout.String() != helpTexts["roster"] {
		t.Fatalf("roster help =\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("help wrote to stderr: %q", stderr.String())
	}
}
