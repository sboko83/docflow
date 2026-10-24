package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fallbackExecutorRunFixture(t *testing.T) *runFixture {
	t.Helper()
	f := executorRunFixture(t)
	text := strings.ReplaceAll(f.read(planFile), "executor: claude | claude-opus-5-5 | high", "executor: pi | zai/glm-5.3 | max")
	text = strings.ReplaceAll(text, "Executor: codex | gpt-5.5 | -\n", "")
	text = strings.ReplaceAll(text, "status: active\n", "status: active\nfallback: codex | gpt-5.5 | -\n")
	f.write(planFile, text)
	f.commit("set fallback routes")
	fake, err := filepath.Abs("../agentio/testdata/fake_agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.tmp, "bin", "pi")
	f.write(path, "#!/bin/sh\nexec /bin/bash "+shellQuote(fake)+" \"$@\"\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func piLimitStep(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "agentio", "testdata", "pi-limit-zai.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return "printf partial > partial.txt\nprintf '%s\\n' " + shellQuote(`{"type":"session","id":"pi-session"}`) + "\nprintf '%s' " + shellQuote(string(data)) + "\n"
}

func piOutageStep() string {
	return emit(map[string]any{"type": "message_end", "message": map[string]any{
		"role": "assistant", "content": []any{}, "stopReason": "error", "errorMessage": "503: upstream unavailable",
	}}) + emit(map[string]any{"type": "agent_settled", "aborted": false})
}

func codexFallbackSuccessStep(f *runFixture) string {
	return strings.ReplaceAll(codexStep(f, 1, 20, 30, true), "u1.txt", "a.txt")
}

func TestRunFallbackOnLimit(t *testing.T) {
	f := fallbackExecutorRunFixture(t)
	f.steps(piLimitStep(t), codexFallbackSuccessStep(f))
	r := f.cli("run", "--no-wait", "--attempts", "1", "--max-units", "1")
	f.check(r, 0, "P001.1.1 done:", "limit until", "switching to codex gpt-5.5", "via codex gpt-5.5", "quota: codex")
	calls := f.calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want pi refusal then codex success", len(calls))
	}
	if !strings.Contains(strings.Join(calls[0].Argv, " "), "zai/glm-5.3") || !strings.Contains(strings.Join(calls[1].Argv, " "), "gpt-5.5") {
		t.Fatalf("routes = %v", calls)
	}
	if strings.Contains(strings.Join(calls[1].Argv, " "), "pi-session") {
		t.Fatalf("pi session was passed to codex: %v", calls[1].Argv)
	}
	if !strings.Contains(calls[1].Stdin, "## Previous attempt") || !strings.Contains(calls[1].Stdin, "continue it") {
		t.Fatalf("codex did not receive full previous-attempt prompt:\n%s", calls[1].Stdin)
	}
	if !strings.Contains(r.out+r.err, "1 attempt,") {
		t.Fatalf("provider refusal spent a unit attempt:\n%s%s", r.out, r.err)
	}
	if got := f.read("partial.txt"); got != "partial" {
		t.Fatalf("work from the refused attempt was lost: %q", got)
	}
}

func TestRunFallbackOnOutage(t *testing.T) {
	f := fallbackExecutorRunFixture(t)
	f.steps(piOutageStep(), codexFallbackSuccessStep(f))
	r := f.cli("run", "--no-wait", "--attempts", "1", "--max-units", "1")
	f.check(r, 0, "outage until", "switching to codex gpt-5.5", "via codex gpt-5.5")
	if len(f.calls()) != 2 {
		t.Fatalf("calls = %d, want fallback codex call", len(f.calls()))
	}
}

func TestRunAllExhaustedNoWait(t *testing.T) {
	f := fallbackExecutorRunFixture(t)
	codexOutage := emit(map[string]any{"type": "thread.started", "thread_id": "t-outage"}) + emit(map[string]any{"type": "turn.failed", "error": map[string]any{"message": "HTTP 503"}})
	f.steps(piLimitStep(t), codexOutage)
	r := f.cli("run", "--no-wait", "--attempts", "1", "--max-units", "1")
	f.check(r, 7, "all executors unavailable until", "the unit stays [~]")
	if !strings.Contains(f.read(planFile), "### [~] P001.1.1") {
		t.Fatalf("unit status changed after --no-wait:\n%s", f.read(planFile))
	}
	if len(f.calls()) != 2 {
		t.Fatalf("calls = %d, want both executors", len(f.calls()))
	}
}
