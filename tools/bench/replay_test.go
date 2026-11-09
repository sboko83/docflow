package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func replayFixture(t *testing.T) replayOptions {
	t.Helper()
	repo := initBenchGit(t)
	writeBenchFile(t, repo, "go.mod", "module tinybase\n\ngo 1.26.0\n")
	writeBenchFile(t, repo, "value.go", "package tinybase\nfunc Value() int { return 1 }\n")
	writeBenchFile(t, repo, "base_test.go", "package tinybase\nimport \"testing\"\nfunc TestPositive(t *testing.T) { if Value() < 1 { t.Fatal(Value()) } }\n")
	writeBenchFile(t, repo, "docs/docflow.md", "---\ndocflow: 3\nread_budget: 40000\n---\n## Queue\n\n## Frozen\n\n## Backlog\n- Make Value return 2.\n  Keep its signature.\n")
	gitBench(t, repo, "add", "-A")
	gitBench(t, repo, "commit", "-m", "before plan")
	from := strings.TrimSpace(gitBench(t, repo, "rev-parse", "HEAD"))
	plan, err := os.ReadFile(filepath.Join("testdata", "tasks", "tiny", "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	path := "docs/plans/P001-value.md"
	writeBenchFile(t, repo, path, string(plan))
	gitBench(t, repo, "add", "-A")
	gitBench(t, repo, "commit", "-m", "add plan")
	writeBenchFile(t, repo, "value.go", "package tinybase\nfunc Value() int { return 2 }\n")
	writeBenchFile(t, repo, "value_test.go", "package tinybase\nimport \"testing\"\nfunc TestExact(t *testing.T) { if Value() != 2 { t.Fatal(Value()) } }\n")
	gitBench(t, repo, "add", "-A")
	gitBench(t, repo, "commit", "-m", "implement task")
	if err := os.Remove(filepath.Join(repo, filepath.FromSlash(path))); err != nil {
		t.Fatal(err)
	}
	writeBenchFile(t, repo, "docs/archive/P001-value.md", "---\nid: P001\ntitle: Value\ntype: feature\nstatus: closed\ncreated: 2026-10-29\nclosed: 2026-11-09\n---\n## Outcome\nValue returns 2.\n## Decisions\nKeep signature.\n## Gotchas\nNone.\n## Follow-ups\nNone.\n")
	gitBench(t, repo, "add", "-A")
	gitBench(t, repo, "commit", "-m", "close plan")
	to := strings.TrimSpace(gitBench(t, repo, "rev-parse", "HEAD"))
	writeBenchFile(t, repo, "private-untracked.txt", "must not leak\n")
	return replayOptions{repo: repo, from: from, to: to, plan: path, oracle: "go test ./..."}
}

func TestReplaySnapshotsAndHiddenOracle(t *testing.T) {
	r := replayFixture(t)
	work := t.TempDir()
	task, err := r.prepare("", work, "replay-value")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".git", "private-untracked.txt", "value_test.go", "docs/archive/P001-value.md", r.plan} {
		if _, err := os.Stat(filepath.Join(task.base, filepath.FromSlash(path))); !os.IsNotExist(err) {
			t.Fatalf("base leaks %s: %v", path, err)
		}
	}
	if !strings.Contains(task.plan, "[ ] P001.1.1") || strings.Contains(task.plan, "Handoff:") {
		t.Fatalf("not the initial plan: %s", task.plan)
	}
	if _, err := os.Stat(filepath.Join(work, "oracle", "value_test.go")); err != nil {
		t.Fatal(err)
	}
	branch := filepath.Join(t.TempDir(), "branch")
	if err := materialize(task.base, branch); err != nil {
		t.Fatal(err)
	}
	if n := strings.TrimSpace(gitBench(t, branch, "rev-list", "--all", "--count")); n != "1" {
		t.Fatalf("copied history: %s", n)
	}
	if remotes := strings.TrimSpace(gitBench(t, branch, "remote")); remotes != "" {
		t.Fatalf("copied remote: %s", remotes)
	}
	cmd := exec.Command("git", "cat-file", "-e", r.to)
	cmd.Dir = branch
	if err := cmd.Run(); err == nil {
		t.Fatal("closing commit is accessible")
	}
	var out bytes.Buffer
	if err := validateReplay("", t.TempDir(), "replay-value", r, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "base 0/1, solution 1/1") {
		t.Fatal(out.String())
	}
	r.backlog = 1
	b, err := r.prepare("", t.TempDir(), "backlog-value")
	if err != nil {
		t.Fatal(err)
	}
	if b.backlog != "Make Value return 2.\nKeep its signature." || b.digest == task.digest {
		t.Fatalf("backlog input/digest: %+v", b)
	}
}

func TestReplayRejectsInvalidBoundaries(t *testing.T) {
	r := replayFixture(t)
	for _, change := range []func(*replayOptions){
		func(r *replayOptions) { r.from = r.to },
		func(r *replayOptions) { r.to = r.from },
		func(r *replayOptions) { r.backlog = 2 },
	} {
		bad := r
		change(&bad)
		if _, err := bad.inspect(""); err == nil {
			t.Fatalf("accepted invalid replay: %+v", bad)
		}
	}
	bad := r
	bad.plan = "../../escape.md"
	if err := bad.check(); err == nil {
		t.Fatal("accepted escaping plan path")
	}
}

func TestAcceptanceReplayArms(t *testing.T) {
	e := newAcceptanceEnv(t)
	r := replayFixture(t)
	writeAcceptanceFile(t, e.binDir, "fakeagent.mode", "replay", 0o644)
	common := []string{"--task", "replay-value", "--replay", r.repo, "--from", r.from, "--to", r.to, "--plan", r.plan}
	for _, args := range [][]string{append([]string{"validate"}, common...), append(append([]string{"run"}, common...), "--dry-run")} {
		out, errOut, code := e.run(args...)
		if code != 0 {
			t.Fatalf("%s: exit %d\n%s\n%s", args[0], code, out, errOut)
		}
	}
	for _, path := range []string{e.results, filepath.Join(e.root, "work")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("validation/dry run wrote %s: %v", path, err)
		}
	}
	for _, backlog := range []bool{false, true} {
		name := "replay-plan"
		if backlog {
			name = "replay-backlog"
		}
		args := []string{"run", "--task", name, "--replay", r.repo, "--from", r.from, "--to", r.to, "--plan", r.plan, "--arms", "single,docflow,docflow-one", "--single-route", "pi | test-model | high", "--docflow", e.docflow, "--timeout", "120"}
		if backlog {
			args = append(args, "--backlog", "1")
		}
		out, errOut, code := e.run(args...)
		if code != 0 {
			t.Fatalf("replay exit %d\n%s\n%s", code, out, errOut)
		}
		rows := e.rows()
		for _, row := range rows[len(rows)-3:] {
			if row.Status != "done" || row.Oracle != (oracleResult{Passed: 1, Total: 1}) {
				log, _ := os.ReadFile(filepath.Join(e.root, "work", "bench", row.RunID, row.Task+"-"+row.Arm, ".bench-docflow.log"))
				t.Fatalf("bad replay result: %+v\n%s\n%s\n%s", row, out, errOut, log)
			}
			if backlog && row.Arm != "single" && (row.Cost == nil || *row.Cost != 0.5) {
				t.Fatalf("planning cost was omitted: %+v", row)
			}
		}
		// The tested source is read-only, including its untracked files.
		if head := strings.TrimSpace(gitBench(t, r.repo, "rev-parse", "HEAD")); head != r.to {
			t.Fatalf("source HEAD changed: %s", head)
		}
	}
}

func TestReplayValidationRejectsVacuousCheck(t *testing.T) {
	r := replayFixture(t)
	r.oracle = "true"
	if err := validateReplay("", t.TempDir(), "vacuous", r, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "oracle passes on base") {
		t.Fatalf("vacuous check accepted: %v", err)
	}
	r.oracle = "false"
	if err := validateReplay("", t.TempDir(), "broken", r, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "before oracle") {
		t.Fatalf("broken baseline accepted: %v", err)
	}
}
