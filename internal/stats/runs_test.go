package stats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunsCommandFoldsEvents(t *testing.T) {
	root, _ := statsProject(t)
	runs := filepath.Join(root, ".git", "docflow", "run")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	events := strings.Join([]string{
		`{"event":"run_start","version":"0.24.0"}`,
		`{"event":"attempt","unit":"P001.1.1","n":1}`,
		`{"event":"verify","unit":"P001.1.1","exit_code":1}`,
		`{"event":"unit_end","unit":"P001.1.1","status":"blocked","seconds":2.5,"test_changes":0}`,
		`{"event":"attempt","unit":"P001.1.1","n":1,"replan":true}`,
		`{"event":"replan","unit":"P001.1.1","result":"done"}`,
		`{"event":"attempt","unit":"P001.1.1","n":1}`,
		`{"event":"verify","unit":"P001.1.1","exit_code":0}`,
		`{"event":"unit_end","unit":"P001.1.1","status":"done","seconds":1.5,"cost":0.25,"test_changes":2,"unverified":3}`,
		`{"event":"close","plan":"P001"}`,
		`{"event":"attempt","unit":"P002.1.1","n":1}`,
		`not json`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(runs, "20261105T000000Z.events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := RunsCommand(root, "P001", false)
	if err != nil {
		t.Fatal(err)
	}
	want := "unit version attempts verify_fails replans blocked test_changes seconds cost unverified done\n" +
		"P001.1.1 0.24.0 2 1 1 1 2 4.0 0.2500 3 yes\n"
	if result.Code != 0 || result.Stdout != want {
		t.Fatalf("RunsCommand() = %#v, want %q", result, want)
	}
	// A stream from a docflow without the unverified key folds to zero.
	empty, err := RunsCommand(root, "P009", false)
	if err != nil || empty.Code == 0 || !strings.Contains(empty.Stderr, "no runs") {
		t.Fatalf("no events: %#v, %v", empty, err)
	}
}

func TestRunsCommandJSON(t *testing.T) {
	root, _ := statsProject(t)
	runs := filepath.Join(root, ".git", "docflow", "run")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	events := strings.Join([]string{
		`{"event":"run_start","version":"0.24.0"}`,
		`{"event":"attempt","unit":"P001.1.1","n":1}`,
		`{"event":"unit_end","unit":"P001.1.1","status":"done","seconds":1.5,"cost":0.25,"test_changes":2,"unverified":1}`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(runs, "20261105T000000Z.events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := RunsCommand(root, "", true)
	if err != nil || result.Code != 0 {
		t.Fatalf("RunsCommand(json) = %#v, %v", result, err)
	}
	var report RunsReport
	if err := json.Unmarshal([]byte(result.Stdout), &report); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, result.Stdout)
	}
	if len(report.Units) != 1 {
		t.Fatalf("units = %d, want 1:\n%s", len(report.Units), result.Stdout)
	}
	row := report.Units[0]
	if row.Unit != "P001.1.1" || !row.Done || row.Unverified != 1 || row.TestChanges != 2 || row.Seconds != 1.5 || row.Cost != 0.25 || row.Version != "0.24.0" {
		t.Fatalf("row = %+v", row)
	}
}
