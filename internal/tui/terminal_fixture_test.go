package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"bsv-docflow/internal/testshim"
)

// The runnable project is shared by the Unix PTY scenarios and the Windows
// console scenario: one plan, one fake agent driven by a step directory.

func writeRunnableProject(t *testing.T) (project, config, scenario string) {
	t.Helper()
	return writeRunnableProjectVerify(t, "test -f a.txt")
}

func writeRunnableProjectVerify(t *testing.T, verify string) (project, config, scenario string) {
	t.Helper()
	result := `{"type":"result","is_error":false,"result":"ok","session_id":"s1","structured_output":{"status":"done","handoff":["done"]}}`
	return writeRunnableProjectAgent(t, verify, result, "sleep 5\n")
}

// writeBlockedProject prepares a runnable project whose fake agent reports a
// human block, which makes the runner write `[!]` and a Handoff item through
// `docflow block` while it is stopping.
func writeBlockedProject(t *testing.T) (project, config, scenario string) {
	t.Helper()
	result := `{"type":"result","is_error":false,"result":"blocked","session_id":"s1","structured_output":{"status":"blocked","blocker":"human","reason":"needs a person"}}`
	return writeRunnableProjectAgent(t, "true", result, "")
}

func writeRunnableProjectAgent(t *testing.T, verify, result, prelude string) (project, config, scenario string) {
	t.Helper()
	project = t.TempDir()
	write := func(path, text string) {
		t.Helper()
		full := filepath.Join(project, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/docflow.md", "---\ndocflow: 3\n---\n## Queue\n- P001\n\n## Frozen\n\n## Backlog\n")
	write("docs/plans/P001-tui.md", `---
id: P001
title: TUI run
status: active
created: 2026-11-01
---
## Goal

Exercise the interactive runner.

## Phase 1: Run

### [ ] P001.1.1 Live control
Do:
- write a.txt
Verify: `+"`"+verify+"`"+`
Commit: P001.1.1: live control
`)
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.name", "t"}, {"config", "user.email", "t@example.invalid"},
		{"config", "commit.gpgsign", "false"}, {"add", "-A"}, {"commit", "-qm", "init"},
	} {
		command := exec.Command("git", args...)
		command.Dir = project
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	scenario = filepath.Join(t.TempDir(), "steps")
	if err := os.MkdirAll(scenario, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scenario, "0.sh"), []byte(prelude+"printf a > a.txt\nprintf '%s\\n' '"+result+"'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scenario, "1.sh"), []byte("printf a > a.txt\nprintf '%s\\n' '"+result+"'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake, err := filepath.Abs("../agentio/testdata/fake_agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	config = filepath.Join(t.TempDir(), "agents.toml")
	body := "[tiers]\ndefault = {agent = \"fake\", model = \"\"}\n[agents.fake]\ncmd = [" +
		strconv.Quote(testshim.Bash(t)) + ", " + strconv.Quote(filepath.ToSlash(fake)) + "]\n" +
		"result = \"json:structured_output\"\ntext = \"json:result\"\nsession = \"json:session_id\"\n"
	if err := os.WriteFile(config, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return project, config, scenario
}
