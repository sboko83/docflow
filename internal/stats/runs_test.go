package stats

import (
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
		`{"event":"attempt","unit":"P001.1.1","n":1}`,
		`{"event":"verify","unit":"P001.1.1","exit_code":1}`,
		`{"event":"unit_end","unit":"P001.1.1","status":"blocked","seconds":2.5,"test_changes":0}`,
		`{"event":"attempt","unit":"P001.1.1","n":1,"replan":true}`,
		`{"event":"replan","unit":"P001.1.1","result":"done"}`,
		`{"event":"attempt","unit":"P001.1.1","n":1}`,
		`{"event":"verify","unit":"P001.1.1","exit_code":0}`,
		`{"event":"unit_end","unit":"P001.1.1","status":"done","seconds":1.5,"cost":0.25,"test_changes":2}`,
		`{"event":"close","plan":"P001"}`,
		`{"event":"attempt","unit":"P002.1.1","n":1}`,
		`not json`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(runs, "20261105T000000Z.events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := RunsCommand(root, "P001")
	if err != nil {
		t.Fatal(err)
	}
	want := "unit attempts verify_fails replans blocked test_changes seconds cost done\n" +
		"P001.1.1 2 1 1 1 2 4.0 0.2500 yes\n"
	if result.Code != 0 || result.Stdout != want {
		t.Fatalf("RunsCommand() = %#v, want %q", result, want)
	}
	empty, err := RunsCommand(root, "P009")
	if err != nil || empty.Code == 0 || !strings.Contains(empty.Stderr, "no runs") {
		t.Fatalf("no events: %#v, %v", empty, err)
	}
}
