package stats

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
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
	want := "unit version attempts verify_fails replans blocked test_changes seconds cost unverified rework done\n" +
		"P001.1.1 0.24.0 2 1 1 1 2 4.0 0.2500 3 - yes\n"
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

// gitAt commits the whole tree of root with subject at the given time.
func gitAt(t *testing.T, root, subject string, at time.Time) string {
	t.Helper()
	stamp := at.Format(time.RFC3339)
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=docflow-tests", "commit", "-q", "-m", subject}} {
		command := exec.Command("git", args...)
		command.Dir = root
		command.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	command := exec.Command("git", "rev-parse", "--short", "HEAD")
	command.Dir = root
	sha, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(sha))
}

// Rework counts commits outside docflow within seven days that change a line
// the unit wrote; an append to the same file, a docflow commit, plan files and
// a later commit do not count.
func TestRunsCommandRework(t *testing.T) {
	root, _ := statsProject(t)
	write := func(name, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	day := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	write("CHANGELOG.md", "# Changelog\n")
	write("a.go", "package a\n")
	gitAt(t, root, "base", day)
	write("a.go", "package a\n\nfunc A() int { return 1 }\n")
	write("CHANGELOG.md", "# Changelog\n- A\n")
	write("docs/plans/P001-x.md", "plan\n")
	unit := gitAt(t, root, "P001.1.1: add A", day)
	write("CHANGELOG.md", "# Changelog\n- A\n- B\n")
	gitAt(t, root, "Note B in the changelog", day.Add(time.Hour)) // append only
	write("docs/plans/P001-x.md", "plan edited\n")
	gitAt(t, root, "Touch the plan", day.Add(2*time.Hour)) // plan file
	write("a.go", "package a\n\nfunc A() int { return 2 }\n")
	gitAt(t, root, "Fix A by hand", day.Add(24*time.Hour)) // rework
	write("a.go", "package a\n\nfunc A() int { return 3 }\n")
	gitAt(t, root, "P001.1.2: tune A", day.Add(48*time.Hour)) // docflow
	write("a.go", "package a\n\n// A is one.\nfunc A() int { return 3 }\n")
	gitAt(t, root, "Comment A", day.Add(72*time.Hour)) // insertion only
	write("CHANGELOG.md", "# Changelog\n- A, fixed\n- B\n")
	gitAt(t, root, "Reword A late", day.Add(8*24*time.Hour)) // past the window

	runs := filepath.Join(root, ".git", "docflow", "run")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	events := strings.Join([]string{
		`{"event":"attempt","unit":"P001.1.1","n":1}`,
		`{"event":"commit","unit":"P001.1.1","sha":"` + unit + `"}`,
		`{"event":"unit_end","unit":"P001.1.1","status":"done"}`,
		`{"event":"commit","unit":"P001.1.2","sha":"0000000"}`,
		`{"event":"unit_end","unit":"P001.1.2","status":"done"}`,
		`{"event":"attempt","unit":"P001.1.3","n":1}`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(runs, "20261101T000000Z.events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := RunsCommand(root, "P001", true)
	if err != nil || result.Code != 0 {
		t.Fatalf("RunsCommand = %#v, %v", result, err)
	}
	var report RunsReport
	if err := json.Unmarshal([]byte(result.Stdout), &report); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, row := range report.Units {
		got[row.Unit] = "nil"
		if row.Rework != nil {
			got[row.Unit] = strconv.Itoa(*row.Rework)
		}
	}
	// P001.1.2's commit is not in the repository; P001.1.3 never committed.
	want := map[string]string{"P001.1.1": "1", "P001.1.2": "nil", "P001.1.3": "nil"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rework = %v, want %v", got, want)
	}
}

// A unit of a plan with `repos:` counts rework in each nested repository its
// root commit names in `Repo:` lines; a named repository that is gone adds
// nothing.
func TestRunsCommandReworkNestedRepo(t *testing.T) {
	root, _ := statsProject(t)
	mod := filepath.Join(root, "mod")
	write := func(name, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	day := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	write(filepath.Join(root, ".gitignore"), "/mod/\n")
	gitAt(t, root, "base", day)
	write(filepath.Join(mod, "b.go"), "package b\n")
	command := exec.Command("git", "init", "-q")
	command.Dir = mod
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	gitAt(t, mod, "base", day)
	write(filepath.Join(mod, "b.go"), "package b\n\nfunc B() int { return 1 }\n")
	nested := gitAt(t, mod, "P001.1.1: add B", day)
	write(filepath.Join(root, "docs", "plans", "P001-x.md"), "plan\n")
	unit := gitAt(t, root, "P001.1.1: add B\n\nRepo: mod "+nested+"\nRepo: gone 1234567", day)
	write(filepath.Join(mod, "b.go"), "package b\n\nfunc B() int { return 2 }\n")
	gitAt(t, mod, "Fix B by hand", day.Add(24*time.Hour)) // rework in the nested repo

	runs := filepath.Join(root, ".git", "docflow", "run")
	write(filepath.Join(runs, "20261101T000000Z.events.jsonl"), strings.Join([]string{
		`{"event":"attempt","unit":"P001.1.1","n":1}`,
		`{"event":"commit","unit":"P001.1.1","sha":"` + unit + `"}`,
		`{"event":"unit_end","unit":"P001.1.1","status":"done"}`,
		"",
	}, "\n"))
	result, err := RunsCommand(root, "P001", true)
	if err != nil || result.Code != 0 {
		t.Fatalf("RunsCommand = %#v, %v", result, err)
	}
	var report RunsReport
	if err := json.Unmarshal([]byte(result.Stdout), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Units) != 1 || report.Units[0].Rework == nil || *report.Units[0].Rework != 1 {
		t.Fatalf("units = %#v, want P001.1.1 with rework 1", report.Units)
	}
}
