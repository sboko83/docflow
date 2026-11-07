package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"bsv-docflow/internal/runner"
)

func TestProgressBar(t *testing.T) {
	if got := progressBar(2, 4, 8); got != "████░░░░" {
		t.Fatalf("progressBar = %q", got)
	}
	if got := progressBar(0, 0, 4); got != "░░░░" {
		t.Fatalf("empty progressBar = %q", got)
	}
}

func TestEditorCommandQuoting(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{`code --wait`, []string{"code", "--wait"}},
		{`'/Applications/Code Editor/bin/code' --wait`, []string{"/Applications/Code Editor/bin/code", "--wait"}},
		{`"C:\Program Files\Editor\editor.exe" --wait`, []string{`C:\Program Files\Editor\editor.exe`, "--wait"}},
		{`/tmp/editor\ tool --wait`, []string{"/tmp/editor tool", "--wait"}},
		{`code --title "a \"quoted\" title" ""`, []string{"code", "--title", `a "quoted" title`, ""}},
	} {
		got, err := splitEditorCommand(tc.command)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("editor %q = %q, %v; want %q", tc.command, got, err, tc.want)
		}
	}
	if _, err := splitEditorCommand(`"unfinished editor`); err == nil {
		t.Fatal("an unclosed editor quote was accepted")
	}
}

func TestProjectActionsRefuseLiveRun(t *testing.T) {
	root := t.TempDir()
	m := newModel(root, func() (Snapshot, error) { return Snapshot{}, nil }, true)
	m.runState = runner.StateRunning
	m.lockCheck = func(string) (int, bool, error) { return 0, false, nil }
	m.dialogInput = "new-plan"
	created, _ := m.createPlan()
	if created.notice != "a live run prevents creating a plan" {
		t.Fatalf("createPlan notice = %q", created.notice)
	}
	m.dialogInput = "P001: commit"
	committed := m.commitProjectFiles()
	if committed.notice != "a live run prevents committing" {
		t.Fatalf("commitProjectFiles notice = %q", committed.notice)
	}
}

func TestProjectCommitIncludesOnlyDocflowPaths(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	git("config", "user.name", "TUI Test")
	git("config", "user.email", "tui@example.invalid")
	for path, body := range map[string]string{"docs/docflow.md": "Queue\n", "docs/plans/P001-demo.md": "draft\n", "app.txt": "base\n"} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "--all")
	git("commit", "-qm", "base")
	if err := os.WriteFile(filepath.Join(root, "docs/plans/P001-demo.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.txt"), []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newModel(root, func() (Snapshot, error) { return Snapshot{}, nil }, true)
	m.notice = "commit: P001: apply roster"
	m.lockCheck = func(string) (int, bool, error) { return 0, false, nil }
	m, _ = m.openProjectCommitDialog()
	if m.dialogInput != "P001: apply roster" {
		t.Fatalf("suggested commit = %q", m.dialogInput)
	}
	m = m.commitProjectFiles()
	if m.notice != "committed docflow files" {
		t.Fatalf("notice = %q", m.notice)
	}
	committed := strings.TrimSpace(git("diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"))
	if committed != "docs/plans/P001-demo.md" {
		t.Fatalf("committed paths = %q", committed)
	}
	status := git("status", "--short")
	if !strings.Contains(status, "app.txt") || strings.Contains(status, "P001-demo.md") {
		t.Fatalf("status after commit = %q", status)
	}
}

func TestProjectCommitHandlesRenamesAndUnicodePaths(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	git("config", "user.name", "TUI Test")
	git("config", "user.email", "tui@example.invalid")
	old := "docs/plans/P001-old.md"
	if err := os.MkdirAll(filepath.Join(root, "docs/plans"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, old), []byte("plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "--all")
	git("commit", "-qm", "base")
	renamed := "docs/plans/P001-renamed.md"
	git("mv", old, renamed)
	unicode := "docs/plans/P002-задача.md"
	if err := os.WriteFile(filepath.Join(root, unicode), []byte("draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside.txt"), []byte("unrelated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "outside.txt")
	m := newModel(root, func() (Snapshot, error) { return Snapshot{}, nil }, true)
	m.dialogInput = "Update project plans"
	m.lockCheck = func(string) (int, bool, error) { return 0, false, nil }
	m = m.commitProjectFiles()
	if m.notice != "committed docflow files" {
		t.Fatalf("notice = %q", m.notice)
	}
	for _, path := range []string{renamed, unicode} {
		git("cat-file", "-e", "HEAD:"+path)
	}
	status := git("status", "--porcelain", "-z")
	if status != "A  outside.txt\x00" {
		t.Fatalf("status after commit = %q", status)
	}
}

func TestCommitMessageProposal(t *testing.T) {
	m := newModel(t.TempDir(), func() (Snapshot, error) { return Snapshot{}, nil }, true)
	for notice, want := range map[string]string{
		"commit: P001: apply roster":                      "P001: apply roster",
		"parameters updated · commit: P001: set timeout":  "P001: set timeout",
		"Backlog saved · commit: docflow: update Backlog": "docflow: update Backlog",
		"plans reloaded": "docflow: update project files",
	} {
		m.notice = notice
		if got := lastCommitMessage(m); got != want {
			t.Fatalf("notice %q proposes %q, want %q", notice, got, want)
		}
	}
	if got := parameterCommitHint(Parameter{Scope: "Plan P007", Key: "gate"}); got != "commit: P007: set gate" {
		t.Fatalf("plan hint = %q", got)
	}
	if got := parameterCommitHint(Parameter{Scope: "Project", Key: "read_budget"}); got != "commit: docflow: set read_budget" {
		t.Fatalf("project hint = %q", got)
	}
}
