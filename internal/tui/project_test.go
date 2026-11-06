package tui

import (
	"os"
	"os/exec"
	"path/filepath"
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
	m.dialogInput = "P001: update plan"
	m.lockCheck = func(string) (int, bool, error) { return 0, false, nil }
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
