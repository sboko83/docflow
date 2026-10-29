package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateTiny(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(root, "tools", "bench", "testdata")
	var output bytes.Buffer
	err = validateCommand([]string{"--task", "tiny", "--tasks", filepath.Join(fixture, "tasks"), "--base", filepath.Join(fixture, "base")}, &output)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := output.String(); got != "tiny: base 0/1, solution 1/1, lint ok\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestValidateRejectsOraclePassingOnBase(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(root, "tools", "bench", "testdata")
	tmp := t.TempDir()
	taskDir := filepath.Join(tmp, "tiny")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(filepath.Join(fixture, "tasks", "tiny"), taskDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "oracle", "benchoracle", "value_test.go"), []byte("package benchoracle\nimport (\"testing\"; \"tinybase\")\nfunc TestValue(t *testing.T) { if tinybase.Value()!=1 { t.Fatal(\"wrong\") } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = validateCommand([]string{"--task", "tiny", "--tasks", tmp, "--base", filepath.Join(fixture, "base")}, &output)
	if err == nil {
		t.Fatal("expected oracle-on-base failure")
	}
}

func TestValidateRejectsBadSolution(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(root, "tools", "bench", "testdata")
	tmp := t.TempDir()
	taskDir := filepath.Join(tmp, "tiny")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(filepath.Join(fixture, "tasks", "tiny"), taskDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "solution", "value.go"), []byte("package tinybase\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = validateCommand([]string{"--task", "tiny", "--tasks", tmp, "--base", filepath.Join(fixture, "base")}, &output)
	if err == nil {
		t.Fatal("expected failing solution")
	}
}

func TestRenderTrendShowsTwoRunsAndDelta(t *testing.T) {
	oldCost, newCost := 1.0, 2.0
	rows := []benchResult{
		{V: 1, RunID: "20261028T100000Z", TS: float64(time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC).Unix()), Task: "small", TaskSHA: "same", Arm: "single", DocflowVersion: "v0.18.0", Oracle: oracleResult{Passed: 1, Total: 2}, Cost: &oldCost},
		{V: 1, RunID: "20261029T100000Z", TS: float64(time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC).Unix()), Task: "small", TaskSHA: "same", Arm: "single", DocflowVersion: "v0.18.1", Oracle: oracleResult{Passed: 2, Total: 2}, Cost: &newCost},
	}
	dir := t.TempDir()
	for _, row := range rows {
		if err := appendResult(dir, row); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if err := reportCommand([]string{"--results", dir}, &output); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(generated)
	if strings.Count(got, "| 2026-") != 2 || !strings.Contains(got, "| 100% | +50 pp | 2.00 | +1.00 |") {
		t.Fatalf("trend lacks both rows or expected delta:\n%s", got)
	}
	if strings.Index(got, "2026-10-29") > strings.Index(got, "2026-10-28") {
		t.Fatalf("rows are not newest first:\n%s", got)
	}
}

func TestAppendResultWithoutCost(t *testing.T) {
	dir := t.TempDir()
	row := benchResult{RunID: "r", Task: "small", Arm: "single", Status: "done", Unverified: []string{"see /Users/secret/project/file.go"}}
	if err := appendResult(dir, row); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"cost":null`)) {
		t.Fatalf("cost must be null: %s", raw)
	}
	if bytes.Contains(raw, []byte("/Users/secret")) || !bytes.Contains(raw, []byte("work/bench/")) {
		t.Fatalf("absolute path was not redacted: %s", raw)
	}
	rows, err := readResults(filepath.Join(dir, "runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Cost != nil {
		t.Fatalf("roundtrip = %#v", rows)
	}
}

func TestEnsureResultsCleanRejectsDirtyResults(t *testing.T) {
	root := initBenchGit(t)
	if err := os.MkdirAll(filepath.Join(root, "bench", "results"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bench", "results", "runs.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := ensureResultsClean(root, "bench/results")
	var coded *exitError
	if err == nil || !errors.As(err, &coded) || coded.code != 2 || !strings.Contains(err.Error(), "bench/results") {
		t.Fatalf("dirty results error = %v", err)
	}
}

func TestCommitResultsDoesNotCommitOtherStagedChanges(t *testing.T) {
	root := initBenchGit(t)
	results := filepath.Join(root, "bench", "results")
	if err := os.MkdirAll(results, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(results, "runs.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.txt"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitBench(t, root, "add", "other.txt")
	if err := commitResults(root, "bench/results", "20261029T101500Z"); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(gitBench(t, root, "show", "--pretty=format:", "--name-only", "HEAD"))
	if got != "bench/results/runs.jsonl" {
		t.Fatalf("commit contains %q", got)
	}
	if staged := strings.TrimSpace(gitBench(t, root, "diff", "--cached", "--name-only")); staged != "other.txt" {
		t.Fatalf("remaining staged paths = %q", staged)
	}
}

func initBenchGit(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitBench(t, root, "init")
	gitBench(t, root, "config", "user.name", "bench test")
	gitBench(t, root, "config", "user.email", "bench@test")
	if err := os.WriteFile(filepath.Join(root, "seed"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitBench(t, root, "add", "seed")
	gitBench(t, root, "commit", "-m", "seed")
	return root
}

func gitBench(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
