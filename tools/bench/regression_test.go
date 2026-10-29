package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOracleCountsTopLevelTestsPerPackage(t *testing.T) {
	var lines []string
	for _, e := range []map[string]string{
		{"Action": "pass", "Package": "one", "Test": "TestValue/child"},
		{"Action": "pass", "Package": "one", "Test": "TestValue"},
		{"Action": "fail", "Package": "two", "Test": "TestValue"},
	} {
		b, _ := json.Marshal(e)
		lines = append(lines, string(b))
	}
	if got := parseGoTestJSON([]byte(strings.Join(lines, "\n"))); got != (testCounts{passed: 1, total: 2}) {
		t.Fatalf("counts = %+v, want one pass out of two top-level tests", got)
	}
	dir := t.TempDir()
	writeBenchFile(t, dir, "oracle_test.go", "package oracle\nimport (\"os\";\"testing\")\nfunc TestMain(m *testing.M) { os.Exit(m.Run()) }\nfunc TestValue(t *testing.T) {}\n")
	if n := oracleTestCount(dir); n != 1 {
		t.Fatalf("declared tests = %d, want 1 (excluding TestMain)", n)
	}
}

func TestMetricsReadAttemptUsageAndUnitCommitNotes(t *testing.T) {
	root := initBenchGit(t)
	base := strings.TrimSpace(gitBench(t, root, "rev-parse", "HEAD"))
	writeBenchFile(t, root, "value.go", "package value\n")
	gitBench(t, root, "add", "value.go")
	gitBench(t, root, "commit", "-m", "P001.1.1: implement value", "-m", "Unverified: integration pending")
	writeBenchFile(t, root, "other.txt", "other\n")
	gitBench(t, root, "add", "other.txt")
	gitBench(t, root, "commit", "-m", "unrelated note", "-m", "Unverified: unrelated")
	log := "$ pi\n--- stdout\n" + acceptancePiOutput() + "\n--- stderr\n\n--- exit 0\n"
	writeBenchFile(t, root, ".git/docflow/run/call.out", log)
	writeBenchFile(t, root, ".git/docflow/run/replan.out", log)
	events := `{"event":"run_start","units":3}
{"event":"attempt","unit":"P001.1.1","route":{"agent":"pi"},"log":".git/docflow/run/call.out"}
{"event":"commit","unit":"P001.1.1","test_changes":["value_test.go: -2 lines"]}
{"event":"unit_end","unit":"P001.1.1","status":"done","cost":0.25}
{"event":"attempt","unit":"P001.2.1","replan":true,"route":{"agent":"pi"},"log":".git/docflow/run/replan.out"}
{"event":"replan","check_changes":["P001.2.1 skipped"]}
`
	writeBenchFile(t, root, ".git/docflow/run/run.events.jsonl", events)
	var got benchResult
	collectRunMetrics(root, &got)
	if got.Tokens != 6 || got.Cost == nil || *got.Cost != 0.5 || got.Units.Total != 3 || got.Units.Done != 1 || got.Attempts != 2 || got.TestChanges != 1 || got.CheckChanges != 1 {
		t.Fatalf("metrics = %+v", got)
	}
	if len(got.Unverified) != 1 || got.Unverified[0] != "Unverified: integration pending" {
		t.Fatalf("notes = %v (base %s)", got.Unverified, base)
	}
}

func acceptancePiOutput() string {
	return `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"usage":{"input":3,"cost":{"total":0.25}}}}`
}

func TestDiffIncludesNewFilesWithoutBenchmarkArtifacts(t *testing.T) {
	root := initBenchGit(t)
	base := strings.TrimSpace(gitBench(t, root, "rev-parse", "HEAD"))
	writeBenchFile(t, root, "new.go", "package value\n")
	writeBenchFile(t, root, ".bench-agent.log", "private log\n")
	writeBenchFile(t, root, "benchoracle/new_test.go", "package benchoracle\n")
	if got := diffFromBase(root, base); got != (diffResult{Files: 1, Insertions: 1}) {
		t.Fatalf("diff = %+v", got)
	}
}

func TestOracleFailureTailExcludesPassingTests(t *testing.T) {
	dir := t.TempDir()
	writeBenchFile(t, dir, "go.mod", "module oraclefixture\n\ngo 1.24\n")
	writeBenchFile(t, dir, "benchoracle/oracle_test.go", `package benchoracle
import "testing"
func TestFirst(t *testing.T) { t.Fatal("distinct failure") }
func TestLast(t *testing.T) { t.Log("passing noise") }
`)
	counts, tail := runOracleDetailed(dir)
	text := strings.Join(tail, "\n")
	if counts != (testCounts{passed: 1, total: 2}) || !strings.Contains(text, "distinct failure") || strings.Contains(text, "passing noise") {
		t.Fatalf("counts=%+v tail=%s", counts, text)
	}
	writeBenchFile(t, dir, "benchoracle/oracle_test.go", "package benchoracle\nimport \"testing\"\nfunc TestValue(t *testing.T) {}\n")
	_, tail = runOracleDetailed(dir)
	if len(tail) != 0 {
		t.Fatalf("successful oracle has failure tail: %v", tail)
	}
}

func TestSummaryKeepsTableRowsTogether(t *testing.T) {
	dir := t.TempDir()
	rows := []benchResult{{Task: "tiny", Arm: "single"}, {Task: "tiny", Arm: "docflow"}}
	if err := writeRunSummary(dir, rows, map[string][]string{"tiny/single": {"failure"}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if strings.Index(text, "### tiny") < strings.Index(text, "| tiny | docflow |") {
		t.Fatalf("failure section splits table:\n%s", text)
	}
}

func TestValidateRejectsFailingSolutionTests(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(root, "tools", "bench", "testdata")
	task := filepath.Join(t.TempDir(), "tiny")
	if err := copyTree(filepath.Join(fixture, "tasks", "tiny"), task); err != nil {
		t.Fatal(err)
	}
	writeBenchFile(t, task, "solution/bad_test.go", "package tinybase\nimport \"testing\"\nfunc TestReference(t *testing.T) { t.Fatal(\"broken reference\") }\n")
	_, err = validateTask(root, filepath.Join(fixture, "base"), filepath.Join(root, "bench", "roster.toml"), task, "tiny", false)
	if err == nil || !strings.Contains(err.Error(), "solution tests") {
		t.Fatalf("broken reference validation = %v", err)
	}
}
