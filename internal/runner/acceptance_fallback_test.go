package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The acceptance tests drive the built docflow binary in executor mode with
// claude, codex and pi stubs in PATH: pi is the primary executor, codex and
// claude follow it in fallback:.

func acceptanceFixture(t *testing.T) *runFixture {
	t.Helper()
	f := fallbackExecutorRunFixture(t)
	f.replace(planFile, "fallback: codex | gpt-5.5 | -", "fallback: codex | gpt-5.5 | -; claude | claude-opus-5-5 | high")
	return f
}

func piFailureStep(text string) string {
	return emit(map[string]any{"type": "message_end", "message": map[string]any{
		"role": "assistant", "content": []any{}, "stopReason": "error", "errorMessage": text,
	}}) + emit(map[string]any{"type": "agent_settled", "aborted": false})
}

func callRoutes(f *runFixture) []string {
	var routes []string
	for _, call := range f.calls() {
		argv := strings.Join(call.Argv, " ")
		switch {
		case strings.Contains(argv, "zai/glm-5.3"):
			routes = append(routes, "pi")
		case strings.Contains(argv, "gpt-5.5"):
			routes = append(routes, "codex")
		case strings.Contains(argv, "claude-opus-5-5"):
			routes = append(routes, "claude")
		default:
			routes = append(routes, argv)
		}
	}
	return routes
}

func TestAcceptanceZaiLimitFallsBackToCodex(t *testing.T) {
	f := acceptanceFixture(t)
	f.steps(piLimitStep(t), codexFallbackSuccessStep(f))
	r := f.cli("run", "--no-wait", "--max-units", "1")
	f.check(r, 0, "limit until", "switching to codex gpt-5.5", "P001.1.1 done:")
	if got := strings.Join(callRoutes(f), ","); got != "pi,codex" {
		t.Fatalf("routes = %s, want pi,codex", got)
	}
	if log := f.git("log", "--format=%s"); !strings.Contains(log, "P001.1.1: first unit") {
		t.Fatalf("unit commit is missing:\n%s", log)
	}
	if !strings.Contains(f.read(planFile), "### [x] P001.1.1") {
		t.Fatalf("unit is not done:\n%s", f.read(planFile))
	}
}

func TestAcceptancePiFetchFailedFallsBack(t *testing.T) {
	f := acceptanceFixture(t)
	f.steps(piFailureStep("fetch failed"), codexFallbackSuccessStep(f))
	r := f.cli("run", "--no-wait", "--max-units", "1")
	f.check(r, 0, "outage until", "switching to codex gpt-5.5", "P001.1.1 done:")
	if got := strings.Join(callRoutes(f), ","); got != "pi,codex" {
		t.Fatalf("routes = %s, want pi,codex", got)
	}
}

func TestAcceptanceWholeChainUnavailable(t *testing.T) {
	f := acceptanceFixture(t)
	codexOutage := emit(map[string]any{"type": "thread.started", "thread_id": "t-outage"}) + emit(map[string]any{"type": "turn.failed", "error": map[string]any{"message": "HTTP 503"}})
	claudeLimit := emit(map[string]any{"type": "result", "is_error": true, "result": "API Error: 429 rate_limit_error"}) + "exit 1\n"
	f.steps(piLimitStep(t), codexOutage, claudeLimit)
	r := f.cli("run", "--no-wait", "--max-units", "1")
	f.check(r, 7, "all executors unavailable until", "the unit stays [~]")
	if got := strings.Join(callRoutes(f), ","); got != "pi,codex,claude" {
		t.Fatalf("routes = %s, want the whole chain", got)
	}
	if !strings.Contains(f.read(planFile), "### [~] P001.1.1") {
		t.Fatalf("unit status changed after --no-wait:\n%s", f.read(planFile))
	}
}

func TestAcceptanceTimeoutKillsDetachedGrandchild(t *testing.T) {
	f := newRunFixture(t)
	f.configure("timeout = 3\n")
	first := f.run([]string{"sleep 5\n"}, "--attempts", "1", "--max-units", "1")
	f.check(first, 7, "timed out after 3 s", "the unit stays [~]")
	second := f.run([]string{writeReply("a.txt")}, "--max-units", "1")
	f.check(second, 0, "P001.1.1 done:")
	logs, err := filepath.Glob(filepath.Join(f.root, ".git", "docflow", "run", "P001.1.1.*.out"))
	if err != nil || len(logs) != 2 {
		t.Fatalf("call logs = %v, %v; want timed-out and resumed calls", logs, err)
	}
	firstLog, err := os.ReadFile(filepath.Join(f.root, ".git", "docflow", "run", "P001.1.1.1.out"))
	if err != nil || !strings.Contains(string(firstLog), "(timed out)") {
		t.Fatalf("first call log was overwritten or lost: %q, %v", firstLog, err)
	}
}

func TestAcceptanceAgentVerifyReused(t *testing.T) {
	f := acceptanceFixture(t)
	f.replace(planFile, "status: active", "status: active\ngate: printf x >> .git/verify-count")
	verify := "echo a > a.txt\n" + shellQuote(f.binary) + " verify P001.1.1 >/dev/null\n"
	f.steps(piLimitStep(t), verify+codexFallbackSuccessStep(f))
	r := f.cli("run", "--no-wait", "--max-units", "1")
	f.check(r, 0, "switching to codex gpt-5.5", "P001.1.1: verify reused", "P001.1.1 done:")
	count, err := os.ReadFile(filepath.Join(f.root, ".git", "verify-count"))
	if err != nil || string(count) != "x" {
		t.Fatalf("gate count = %q, %v; want one run", count, err)
	}
	var verifies int
	for _, record := range fixtureEvents(t, f) {
		if record["event"] == "verify" {
			verifies++
			if record["reused"] != true || record["exit_code"] != float64(0) {
				t.Fatalf("reused verify event = %v", record)
			}
		}
	}
	if verifies != 1 {
		t.Fatalf("verify events = %d, want 1", verifies)
	}
}
