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

func TestRunTinySingleAndDocflowArms(t *testing.T) {
	root := t.TempDir()
	configHome := filepath.Join(root, "config")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	if err := os.MkdirAll(filepath.Join(configHome, "docflow"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitBench(t, root, "init")
	gitBench(t, root, "config", "user.name", "bench test")
	gitBench(t, root, "config", "user.email", "bench@test")
	base := filepath.Join(root, "bench", "base")
	taskDir := filepath.Join(root, "bench", "tasks", "tiny")
	writeBenchFile(t, base, "go.mod", "module tiny\n\ngo 1.24\n")
	writeBenchFile(t, base, "value.go", "package tiny\nfunc Value() int { return 1 }\n")
	writeBenchFile(t, taskDir, "plan.md", "---\nid: P001\ntitle: Tiny\ntype: feature\nstatus: active\ncreated: 2026-10-29\n---\n## Goal\nChange value to 2.\n\n## Context\n\n## Constraints\n\n## Traps\n\n## Decisions\n\n## Phase 1: Change\n\n### [ ] P001.1.1 Change value\nRead:\n- value.go\nDo:\n- Change value to 2.\nVerify: `go test ./...`\nCommit: P001.1.1: change value\n")
	writeBenchFile(t, filepath.Join(taskDir, "oracle", "benchoracle"), "value_test.go", `package benchoracle
import ("testing"; "tiny")
func TestValue(t *testing.T) { if tiny.Value() != 2 { t.Fatal("value") } }
`)
	writeBenchFile(t, filepath.Join(taskDir, "solution"), "value.go", "package tiny\nfunc Value() int { return 2 }\n")
	fixture, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	rosterFile := filepath.Join(root, "bench", "roster.toml")
	if err := os.MkdirAll(filepath.Dir(rosterFile), 0o755); err != nil {
		t.Fatal(err)
	}
	rosterText := `spend = ["pi", "codex"]

[provider.pi]
check = false

[role]
cheap = "cheap"
executor = "standard"
heavy = "strong"
review = "strong"
acceptance = "trusted"
replan = "trusted"

[route.pi-cheap]
run = "pi | test-model | high"
tier = "cheap"

[route.pi-standard]
run = "pi | test-model | high"
tier = "standard"

[route.pi-strong]
run = "pi | test-model | high"
tier = "strong"

[route.codex-strong]
run = "codex | gpt-6.1-sol | high"
tier = "strong"

`
	if err := os.WriteFile(rosterFile, []byte(rosterText), 0o644); err != nil {
		t.Fatal(err)
	}
	gitBench(t, root, "add", "-A")
	gitBench(t, root, "commit", "-m", "base")
	binDir := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pi := `#!/bin/sh
case "$*" in
  *'auth print-api-key'*) exit 1 ;;
esac
cat >/dev/null
printf 'package tiny\nfunc Value() int { return 2 }\n' > value.go
printf '%s\n' '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"{\"status\":\"done\",\"handoff\":[\"tiny complete\"],\"decisions\":[],\"unverified\":[],\"reason\":\"\",\"blocker\":\"\"}"}],"usage":{"input":3,"cost":{"total":0.25}}}}'
`
	if err := os.WriteFile(filepath.Join(binDir, "pi"), []byte(pi), 0o755); err != nil {
		t.Fatal(err)
	}
	codex := `#!/bin/sh
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r request; do
    case "$request" in
      *'"id":1'*) printf '%s\n' '{"id":1,"result":{}}' ;;
      *'"id":2'*) printf '%s\n' '{"id":2,"result":{"rateLimits":[]}}'; exit 0 ;;
    esac
  done
  exit 0
fi
cat >/dev/null
printf 'package tiny\nfunc Value() int { return 2 }\n' > value.go
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"done\",\"handoff\":[\"done\"],\"decisions\":[],\"unverified\":[],\"reason\":\"\",\"blocker\":\"\"}"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":3,"output_tokens":4}}'
`
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte(codex), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeDocflow := filepath.Join(root, "fake-docflow")
	build := exec.Command("go", "build", "-o", fakeDocflow, "./cmd/docflow")
	build.Dir = fixture
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build test docflow: %v\\n%s", err, output)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var output bytes.Buffer
	args := []string{"--task", "tiny", "--arms", "single,docflow", "--single-route", "pi | test-model | high", "--roster", "bench/roster.toml", "--docflow", fakeDocflow, "--timeout", "30"}
	if err := runCommandAt(root, args, &output); err != nil {
		t.Fatalf("run: %v\\n%s", err, output.String())
	}
	rows, err := readResults(filepath.Join(root, "bench", "results", "runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows: %#v", len(rows), rows)
	}
	for _, row := range rows {
		if row.Status != "done" || row.Oracle.Passed != 1 || row.Oracle.Total != 1 || row.Cost == nil || *row.Cost != 0.25 {
			branch := filepath.Join(root, "work", "bench", row.RunID, "tiny-"+row.Arm)
			log, _ := os.ReadFile(filepath.Join(branch, ".bench-docflow.log"))
			commits, _ := gitOutput(branch, "log", "--oneline", "-4")
			t.Fatalf("incomplete result for %s: %#v\\nbranch log: %s\\ncommits: %s", row.Arm, row, log, commits)
		}
	}
	var dryRun bytes.Buffer
	beforeDryRun, err := os.ReadFile(filepath.Join(root, "bench", "results", "runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := runCommandAt(root, []string{"--task", "tiny", "--dry-run", "--single-route", "pi | test-model | high", "--roster", "bench/roster.toml"}, &dryRun); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(dryRun.String(), "single: pi | test-model | high") || !strings.Contains(dryRun.String(), "arms: single, docflow") {
		t.Fatalf("dry-run output = %q", dryRun.String())
	}
	afterDryRun, err := os.ReadFile(filepath.Join(root, "bench", "results", "runs.jsonl"))
	if err != nil || string(afterDryRun) != string(beforeDryRun) {
		t.Fatalf("dry-run changed results: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "pi"), []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runCommandAt(root, []string{"--task", "tiny", "--arms", "single", "--single-route", "pi | test-model | high", "--roster", "bench/roster.toml", "--docflow", fakeDocflow, "--timeout", "1"}, &output); err != nil {
		t.Fatalf("timeout run: %v", err)
	}
	rows, err = readResults(filepath.Join(root, "bench", "results", "runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	foundTimeout := false
	for _, row := range rows {
		if row.Arm == "single" && row.Status == "timeout" {
			foundTimeout = true
		}
	}
	if !foundTimeout {
		t.Fatalf("timeout result missing: %#v", rows)
	}
}

func TestDocflowRunCompletedAcceptsNothingToDoExit(t *testing.T) {
	cmd := exec.Command("sh", "-c", "printf 'nothing to do\\n'; exit 1")
	output, err := cmd.CombinedOutput()
	if !docflowRunCompleted(err, output) {
		t.Fatalf("docflow run completion rejected exit 1 with nothing to do: err=%v output=%q", err, output)
	}
}

func TestDocflowRunCompletedRejectsOtherErrors(t *testing.T) {
	for _, script := range []string{"printf 'failed\\n'; exit 1", "printf 'nothing to do\\n'; exit 2"} {
		cmd := exec.Command("sh", "-c", script)
		output, err := cmd.CombinedOutput()
		if docflowRunCompleted(err, output) {
			t.Fatalf("docflow run completion accepted %q: err=%v output=%q", script, err, output)
		}
	}
}

func writeBenchFile(t *testing.T, dir, name, text string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
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
