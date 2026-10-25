package runner

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The batch acceptance tests drive the built docflow binary through one
// scenario per P038 goal: route-switch context, the review unit's phase
// diff, the event stream and run --close.

func TestBatchRouteSwitchCarriesDiffStatAndLastMessage(t *testing.T) {
	f := fallbackExecutorRunFixture(t)
	f.write("tracked.txt", "before\n")
	f.commit("seed tracked file")
	f.steps(piContextLimitStep(t), codexFallbackSuccessStep(f))
	r := f.cli("run", "--no-wait", "--attempts", "1", "--max-units", "1")
	f.check(r, 0, "switching to codex gpt-5.5", "P001.1.1 done:")
	calls := f.calls()
	if got := strings.Join(callRoutes(f), ","); got != "pi,codex" {
		t.Fatalf("routes = %s, want pi,codex", got)
	}
	prompt := calls[1].Stdin
	for _, want := range []string{
		"## Previous attempt",
		"The previous executor pi zai/glm-5.3 stopped after a provider limit refusal.",
		"### Last messages\n\n- I modified tracked.txt and confirmed the local work.\n",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("fallback prompt missing %q:\n%s", want, prompt)
		}
	}
	diffStat := regexp.MustCompile(`### Working tree\n\n(?: \S+ +\| \d+ [+-]+\n)* tracked\.txt +\| 2 \+-\n(?: \S+ +\| \d+ [+-]+\n)* \d+ files? changed`)
	if !diffStat.MatchString(prompt) {
		t.Fatalf("fallback prompt has no diff stat for tracked.txt:\n%s", prompt)
	}
}

func TestBatchReviewPromptHasPhaseDiff(t *testing.T) {
	f := newRunFixture(t)
	f.replace(planFile, "Commit: P001.1.2: second unit\n", "Commit: P001.1.2: second unit\n\n### [ ] P001.1.3 Review unit\nRole: review\nRead:\n- docs/docflow.md\nDo:\n- review the phase\nVerify: `true`\nCommit: P001.1.3: review unit\n")
	base := f.git("rev-parse", "--short", "HEAD")
	f.check(f.run([]string{writeReply("a.txt"), writeReply("b.txt")}, "--max-units", "2"), 0, "P001.1.1 done:", "P001.1.2 done:")
	first := f.git("log", "-1", "--format=%h %s", "--grep", "^P001.1.1: first unit$")
	second := f.git("log", "-1", "--format=%h %s", "--grep", "^P001.1.2: second unit$")
	if first == "" || second == "" {
		t.Fatalf("unit commits are missing:\n%s", f.git("log", "--format=%h %s"))
	}
	r := f.cli("prompt", "P001.1.3")
	f.check(r, 0)
	want := "## Phase diff\n\nBase: " + base + "\n\nCommits:\n" + first + "\n" + second + "\nCommand: `git diff " + base + " HEAD`\n"
	if !strings.Contains(r.out, want) {
		t.Fatalf("review prompt missing %q:\n%s", want, r.out)
	}
}

func TestBatchEventStreamRecordsRouteSwitch(t *testing.T) {
	f := fallbackExecutorRunFixture(t)
	f.steps(piLimitStep(t), codexFallbackSuccessStep(f))
	f.check(f.cli("run", "--no-wait", "--attempts", "1", "--max-units", "1"), 0, "switching to codex gpt-5.5")
	files, err := filepath.Glob(filepath.Join(f.root, ".git", "docflow", "run", "*.events.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("event files = %v, %v", files, err)
	}
	records := readEvents(t, files[0])
	last := len(records) - 1
	if records[0]["event"] != "run_start" || records[last]["event"] != "run_end" {
		t.Fatalf("stream is not bounded by run_start and run_end: %v", records)
	}
	switches := 0
	for _, record := range records[1:last] {
		if record["event"] == "route_switch" {
			switches++
			if record["from"].(map[string]any)["agent"] != "pi" || record["to"].(map[string]any)["agent"] != "codex" {
				t.Fatalf("route_switch = %v", record)
			}
		}
	}
	if switches != 1 {
		t.Fatalf("route_switch events = %d, want 1: %v", switches, records)
	}
}

func TestBatchRunCloseArchivesReadyPlan(t *testing.T) {
	f := readyCloseFixture(t)
	r := f.run([]string{closeArchiveStep + doneReply()}, "--close")
	f.check(r, 0, "P001: closed")
	if _, err := os.Stat(filepath.Join(f.root, planFile)); !os.IsNotExist(err) {
		t.Fatalf("plan still in docs/plans: %v", err)
	}
	if !strings.Contains(f.read("docs/archive/P001-plan.md"), "None.") {
		t.Fatalf("archive entry is not filled:\n%s", f.read("docs/archive/P001-plan.md"))
	}
	if subject := f.git("log", "-1", "--format=%s"); subject != "P001: close plan" {
		t.Fatalf("last commit = %q, want P001: close plan", subject)
	}
	if status := f.git("status", "--porcelain"); status != "" {
		t.Fatalf("closing left a dirty tree:\n%s", status)
	}
}
