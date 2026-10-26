package runner

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/format"
)

func TestRunnerPromptInstructions(t *testing.T) {
	f := newRunFixture(t)
	plan, err := format.ParsePlan(f.read(planFile), planFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"auto", "full"} {
		text, err := runnerPrompt(f.root, plan, plan.Units[0], map[string]any{"instructions": "Be brief.\n"}, mode)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "Be brief.") != (mode == "auto") {
			t.Fatal(text)
		}
		if !strings.Contains(text, "## Cycle (runner mode)") || !strings.Contains(text, "docflow verify P001.1.1") || strings.Contains(text, "docflow done P001.1.1") {
			t.Fatal(text)
		}
		for _, part := range []string{"Handoff `Unblocked:` after the latest `Blocked (human)`", "a legacy `Blocked:` item counts as human", "Before `Do:`", "check that condition with a command first", "naming what is still missing"} {
			if !strings.Contains(text, part) {
				t.Fatalf("runner prompt does not contain %q", part)
			}
		}
	}
	config, err := agents.LoadAgents("", false)
	if err != nil {
		t.Fatal(err)
	}
	agent := profile(config, "codex")
	auto, err := runnerPrompt(f.root, plan, plan.Units[0], agent, "auto")
	if err != nil {
		t.Fatal(err)
	}
	full, err := runnerPrompt(f.root, plan, plan.Units[0], agent, "full")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(auto, "workspace-write sandbox") || strings.Contains(full, "sandbox") {
		t.Fatal("instructions did not follow permissions")
	}
}

func TestPrerequisiteTimeoutAndClosedInput(t *testing.T) {
	f := newRunFixture(t)
	text := strings.Replace(f.read(planFile), "## Traps", "## Prerequisites\n- token: `echo no-token >&2; exit 3`\n- server: `exec sleep 5`\n- input: `read x`\n\n## Traps", 1)
	plan, err := format.ParsePlan(text, planFile)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := runPrerequisitesWithTimeout(f.root, plan, newProgress(&out, &errOut, false), 100*time.Millisecond)
	if code != 2 {
		t.Fatal(code)
	}
	want := "prerequisite failed: token (echo no-token >&2; exit 3, exit 3): no-token\nprerequisite failed: server (exec sleep 5, timeout)\nprerequisite failed: input (read x, exit 1)\n"
	if errOut.String() != want {
		t.Fatalf("%q, want %q", errOut.String(), want)
	}
}

func TestWatchKeysConsumesInputAndStopsOnce(t *testing.T) {
	f := newRunFixture(t)
	var out bytes.Buffer
	reader := strings.NewReader("x\n\n")
	watchKeys(f.root, reader, newProgress(&out, &out, false), nil)
	stop, _ := stopFile(f.root)
	if _, err := os.Stat(stop); err == nil {
		t.Fatal("unknown key stopped the run")
	}
	reader = strings.NewReader("x\n S \ns\n")
	watchKeys(f.root, reader, newProgress(&out, &out, false), nil)
	if _, err := os.Stat(stop); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "received") != 1 || reader.Len() != 0 {
		t.Fatal(out.String())
	}
}

func TestLegacyPendingCommitSurvivesFailedVerify(t *testing.T) {
	f := newRunFixture(t)
	f.check(f.cli("prompt", "P001.1.1"), 0)
	f.write("a.txt", "a")
	f.check(f.cli("verify", "P001.1.1"), 0)
	f.check(f.cli("done", "P001.1.1", "--handoff=x"), 0)
	// Old runners left only a saved fingerprint for an already-done unit.
	if err := os.Remove(filepath.Join(f.root, ".git/docflow/committing/P001.1.1.json")); err != nil {
		t.Fatal(err)
	}
	f.check(f.cli("status"), 0, "current: P001.1.1", "commit pending")
	f.write("docs/docflow.md", strings.Replace(f.read("docs/docflow.md"), "read_budget: 40000", "read_budget: 40000\npreflight: false", 1))
	f.check(f.cli("verify", "P001.1.1"), 4, "preflight failed")
	f.check(f.cli("status"), 0, "current: P001.1.1", "commit pending")
}
