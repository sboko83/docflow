package cycle

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"bsv-docflow/internal/format"
)

const commitPlanPath = "docs/plans/P001-plan.md"

func commitPlan(status string) string {
	return cyclePlan("P001", "active", cycleUnit("P001.1.1", status))
}

func makeCommitProject(t *testing.T, plan string, files map[string]string) string {
	t.Helper()
	return makeCycleProject(t, cycleDocflow("P001"), map[string]string{"P001": plan}, files)
}

func startCommitUnit(t *testing.T, root string) {
	t.Helper()
	result, err := Prompt(root, "P001.1.1", nil)
	if err != nil || result.Code != 0 {
		t.Fatalf("Prompt() = %#v, %v", result, err)
	}
}

func verifyAndDoneCommitUnit(t *testing.T, root string, repos func(string) ([]string, error)) {
	t.Helper()
	if result, err := Verify(root, "P001.1.1", nil, false, repos, nil); err != nil || result.Code != 0 {
		t.Fatalf("Verify() = %#v, %v", result, err)
	}
	if result, err := Done(root, "P001.1.1", []string{"первое", "второе"}, []string{"д"}, repos); err != nil || result.Code != 0 {
		t.Fatalf("Done() = %#v, %v", result, err)
	}
}

func TestCommitMessage(t *testing.T) {
	text := strings.Replace(commitPlan("x"), "Commit: P001.1.1: unit\n", "Commit: P001.1.1: unit\nHandoff:\n- первое\n- второе\n", 1)
	plan, err := format.ParsePlan(text, commitPlanPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := CommitMessage(plan.Unit("P001.1.1")); got != "P001.1.1: unit\n\n- первое\n- второе\n" {
		t.Fatalf("CommitMessage() = %q", got)
	}
	plan, err = format.ParsePlan(commitPlan("x"), commitPlanPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := CommitMessage(plan.Unit("P001.1.1")); got != "P001.1.1: unit\n" {
		t.Fatalf("CommitMessage() without Handoff = %q", got)
	}
}

func TestCommitFullCycle(t *testing.T) {
	root := makeCommitProject(t, commitPlan(" "), map[string]string{"a.py": "print(1)\n"})
	startCommitUnit(t, root)
	writeCycleFile(t, root, "a.py", "print(2)\n")
	writeCycleFile(t, root, "src/new.py", "x = 1\n")
	verifyAndDoneCommitUnit(t, root, nil)

	result, err := Commit(root, "P001.1.1", nil)
	if err != nil || result.Code != 0 {
		t.Fatalf("Commit() = %#v, %v", result, err)
	}
	lines := strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n")
	if !strings.HasSuffix(lines[0], " P001.1.1: unit") {
		t.Fatalf("commit output = %q", result.Stdout)
	}
	sort.Strings(lines[1:])
	wantFiles := []string{commitPlanPath, "a.py", "src/new.py"}
	sort.Strings(wantFiles)
	if strings.Join(lines[1:], "\n") != strings.Join(wantFiles, "\n") {
		t.Fatalf("committed files = %q, want %q", lines[1:], wantFiles)
	}
	if got := gitCycle(t, root, "log", "-1", "--format=%B"); got != "P001.1.1: unit\n\n- первое\n- второе\n\n" {
		t.Fatalf("commit message = %q", got)
	}
	if got := gitCycle(t, root, "status", "--porcelain"); got != "" {
		t.Fatalf("worktree after commit = %q", got)
	}
	if _, found, err := savedFingerprint(root, "P001.1.1"); err != nil || found {
		t.Fatalf("saved fingerprint remains: found=%v err=%v", found, err)
	}
	plan, unit, err := findUnit(root, "P001.1.1")
	if err != nil || plan == nil || unit.Status != "x" {
		t.Fatalf("committed unit = %#v, %v", unit, err)
	}
}

func TestCommitRejectsWrongStatusAndChangedTree(t *testing.T) {
	t.Run("not done", func(t *testing.T) {
		root := makeCommitProject(t, commitPlan(" "), map[string]string{"a.py": "print(1)\n"})
		startCommitUnit(t, root)
		if result, err := Verify(root, "P001.1.1", nil, false, nil, nil); err != nil || result.Code != 0 {
			t.Fatalf("Verify() = %#v, %v", result, err)
		}
		result, err := Commit(root, "P001.1.1", nil)
		if err != nil || result.Code != 5 || !strings.Contains(result.Stderr, "expected [x]") {
			t.Fatalf("Commit() = %#v, %v", result, err)
		}
	})

	t.Run("tree changed after done", func(t *testing.T) {
		root := makeCommitProject(t, commitPlan(" "), map[string]string{"a.py": "print(1)\n"})
		startCommitUnit(t, root)
		writeCycleFile(t, root, "a.py", "print(2)\n")
		verifyAndDoneCommitUnit(t, root, nil)
		writeCycleFile(t, root, "a.py", "print(3)\n")
		result, err := Commit(root, "P001.1.1", nil)
		if err != nil || result.Code != 5 || !strings.Contains(result.Stderr, "tree changed after verify") {
			t.Fatalf("Commit() = %#v, %v", result, err)
		}
		if got := gitCycle(t, root, "log", "--format=%s"); got != "initial\n" {
			t.Fatalf("unexpected commit after changed tree: %q", got)
		}
	})

	t.Run("already committed", func(t *testing.T) {
		root := makeCommitProject(t, commitPlan(" "), map[string]string{"a.py": "print(1)\n"})
		startCommitUnit(t, root)
		verifyAndDoneCommitUnit(t, root, nil)
		if result, err := Commit(root, "P001.1.1", nil); err != nil || result.Code != 0 {
			t.Fatalf("first Commit() = %#v, %v", result, err)
		}
		result, err := Commit(root, "P001.1.1", nil)
		if err != nil || result.Code != 5 {
			t.Fatalf("second Commit() = %#v, %v", result, err)
		}
	})
}

func TestFinish(t *testing.T) {
	t.Run("full cycle", func(t *testing.T) {
		root := makeCommitProject(t, commitPlan(" "), map[string]string{"a.py": "print(1)\n"})
		startCommitUnit(t, root)
		writeCycleFile(t, root, "a.py", "print(2)\n")
		result, err := Finish(root, "P001.1.1", []string{"первое"}, []string{"д"}, nil, nil, nil)
		if err != nil || result.Code != 0 {
			t.Fatalf("Finish() = %#v, %v", result, err)
		}
		lines := strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n")
		if len(lines) != 5 || lines[0] != "P001.1.1: verify passed: true" || lines[1] != "P001.1.1: done" || !strings.HasSuffix(lines[2], " P001.1.1: unit") {
			t.Fatalf("Finish() output = %q", result.Stdout)
		}
		sort.Strings(lines[3:])
		if strings.Join(lines[3:], "\n") != "a.py\ndocs/plans/P001-plan.md" {
			t.Fatalf("Finish() file output = %q", lines[3:])
		}
		if got := gitCycle(t, root, "status", "--porcelain"); got != "" {
			t.Fatalf("worktree after Finish() = %q", got)
		}
	})

	t.Run("verify failure", func(t *testing.T) {
		plan := strings.Replace(commitPlan(" "), "Verify: `true`", "Verify: `false`", 1)
		root := makeCommitProject(t, plan, map[string]string{"a.py": "print(1)\n"})
		startCommitUnit(t, root)
		writeCycleFile(t, root, "a.py", "print(2)\n")
		result, err := Finish(root, "P001.1.1", []string{"h"}, nil, nil, nil, nil)
		if err != nil || result.Code != 4 || !strings.Contains(result.Stdout, "verify failed") {
			t.Fatalf("Finish() = %#v, %v", result, err)
		}
		_, unit, err := findUnit(root, "P001.1.1")
		if err != nil || unit.Status != "~" || gitCycle(t, root, "log", "--format=%s") != "initial\n" {
			t.Fatalf("failed finish changed cycle state: unit=%#v err=%v", unit, err)
		}
	})

	t.Run("skip verify", func(t *testing.T) {
		root := makeCommitProject(t, commitPlan(" "), map[string]string{"a.py": "print(1)\n"})
		startCommitUnit(t, root)
		writeCycleFile(t, root, "a.py", "print(2)\n")
		reason := "нет сети"
		result, err := Finish(root, "P001.1.1", []string{"h"}, nil, &reason, nil, nil)
		if err != nil || result.Code != 0 || !strings.Contains(result.Stdout, "verify skipped: нет сети") {
			t.Fatalf("Finish() with skip = %#v, %v", result, err)
		}
		if !strings.Contains(gitCycle(t, root, "log", "-1", "--format=%B"), "Verify skipped: нет сети") {
			t.Fatal("skip reason was not added to the commit message")
		}
	})

	t.Run("without prompt", func(t *testing.T) {
		root := makeCommitProject(t, commitPlan(" "), map[string]string{"a.py": "print(1)\n"})
		result, err := Finish(root, "P001.1.1", []string{"h"}, nil, nil, nil, nil)
		if err != nil || result.Code != 5 || !strings.Contains(result.Stdout, "verify passed") || !strings.Contains(result.Stderr, "expected [~]") {
			t.Fatalf("Finish() without prompt = %#v, %v", result, err)
		}
	})
}

func TestCommitNestedRepository(t *testing.T) {
	root := makeCommitProject(t, commitPlan(" "), map[string]string{".gitignore": "deps/module/\n", "a.py": "print(1)\n"})
	nested := filepath.Join(root, "deps", "module")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCycle(t, nested, "init", "-q")
	gitCycle(t, nested, "config", "user.name", "docflow test")
	gitCycle(t, nested, "config", "user.email", "test@example.invalid")
	writeCycleFile(t, root, "deps/module/b.py", "print(1)\n")
	gitCycle(t, nested, "add", "-A")
	gitCycle(t, nested, "commit", "-q", "-m", "initial nested")
	repos := func(string) ([]string, error) { return []string{"deps/module"}, nil }
	startCommitUnit(t, root)
	writeCycleFile(t, root, "a.py", "print(2)\n")
	writeCycleFile(t, root, "deps/module/b.py", "print(2)\n")
	verifyAndDoneCommitUnit(t, root, repos)
	result, err := Commit(root, "P001.1.1", repos)
	if err != nil || result.Code != 0 {
		t.Fatalf("Commit() = %#v, %v", result, err)
	}
	if !strings.Contains(result.Stdout, "deps/module ") || !strings.Contains(result.Stdout, "deps/module/b.py\n") {
		t.Fatalf("nested commit output = %q", result.Stdout)
	}
	if got := gitCycle(t, nested, "status", "--porcelain"); got != "" {
		t.Fatalf("nested worktree after commit = %q", got)
	}
	if got := gitCycle(t, root, "status", "--porcelain"); got != "" {
		t.Fatalf("root worktree after commit = %q", got)
	}
	message := gitCycle(t, root, "log", "-1", "--format=%B")
	if !strings.Contains(message, "Repo: deps/module ") {
		t.Fatalf("root commit message lacks nested commit record: %q", message)
	}
}

func TestCommitReplan(t *testing.T) {
	plan := strings.Replace(commitPlan("!"), "### [!] P001.1.1", "### [~] P001.1.1", 1)
	t.Run("plan only", func(t *testing.T) {
		root := makeCommitProject(t, commitPlan("!"), map[string]string{"a.py": "print(1)\n"})
		writeCycleFile(t, root, commitPlanPath, plan)
		writeCycleFile(t, root, "a.py", "print(2)\n")
		writeCycleFile(t, root, "untracked.txt", "new\n")
		sha, err := CommitReplan(root, commitPlanPath, "P001.1.1")
		if err != nil || sha == "" {
			t.Fatalf("CommitReplan() = %q, %v", sha, err)
		}
		if got := strings.TrimSpace(gitCycle(t, root, "log", "-1", "--format=%s")); got != "P001: replan P001.1.1" {
			t.Fatalf("commit subject = %q", got)
		}
		if got := strings.Fields(gitCycle(t, root, "show", "--name-only", "--format=")); len(got) != 1 || got[0] != commitPlanPath {
			t.Fatalf("committed paths = %#v", got)
		}
		status := gitCycle(t, root, "status", "--porcelain")
		if !strings.Contains(status, " M a.py") || !strings.Contains(status, "?? untracked.txt") {
			t.Fatalf("other tree changes were not left behind: %q", status)
		}
	})

	t.Run("staged other files stay staged", func(t *testing.T) {
		root := makeCommitProject(t, commitPlan("!"), map[string]string{"a.py": "print(1)\n"})
		writeCycleFile(t, root, commitPlanPath, plan)
		writeCycleFile(t, root, "a.py", "print(2)\n")
		gitCycle(t, root, "add", "a.py")
		if _, err := CommitReplan(root, commitPlanPath, "P001.1.1"); err != nil {
			t.Fatal(err)
		}
		status := gitCycle(t, root, "status", "--porcelain")
		if !strings.Contains(status, "M  a.py") {
			t.Fatalf("staged change was not preserved: %q", status)
		}
	})

	t.Run("hook failure", func(t *testing.T) {
		root := makeCommitProject(t, commitPlan("!"), map[string]string{"a.py": "print(1)\n"})
		writeCycleFile(t, root, ".git/hooks/pre-commit", "#!/bin/sh\necho 'hook says no' >&2\nexit 1\n")
		if err := os.Chmod(filepath.Join(root, ".git/hooks/pre-commit"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := CommitReplan(root, commitPlanPath, "P001.1.1"); err == nil || errorExitCode(err) != 4 || !strings.Contains(err.Error(), "hook says no") {
			t.Fatalf("CommitReplan() hook error = %v", err)
		}
	})

	t.Run("git failure", func(t *testing.T) {
		root := makeCommitProject(t, commitPlan("!"), map[string]string{"a.py": "print(1)\n"})
		if _, err := CommitReplan(root, "docs/plans/P002-none.md", "P002.1.1"); err == nil || !strings.Contains(err.Error(), "git commit failed:") {
			t.Fatalf("CommitReplan() git error = %v", err)
		}
	})
}

func TestHeadMovedReportsNewCommit(t *testing.T) {
	root := makeCommitProject(t, commitPlan("~"), map[string]string{"a.py": "print(1)\n"})
	before, err := Heads(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeCycleFile(t, root, "a.py", "print(2)\n")
	gitCycle(t, root, "add", "a.py")
	gitCycle(t, root, "commit", "-q", "-m", "agent commit")
	plan, err := formatPlanForTest(t, root)
	if err != nil {
		t.Fatal(err)
	}
	movement, err := HeadMoved(root, before, plan, "P001.1.1", nil)
	if err != nil || movement == nil || movement.Kind != "commits" || len(movement.Commits) != 1 || !strings.HasSuffix(movement.Commits[0], "agent commit") {
		t.Fatalf("HeadMoved() = %#v, %v", movement, err)
	}
}

func formatPlanForTest(t *testing.T, root string) (*format.Plan, error) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(commitPlanPath)))
	if err != nil {
		return nil, err
	}
	return format.ParsePlan(string(content), commitPlanPath)
}
