package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/testshim"
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
	writeFakePi(t, f, fake)
	return f
}

// writeFakePi installs a pi stub that answers the zai quota key request
// without touching the scenario and otherwise delegates to the fake agent;
// without FAKE_ZAI_KEY the stub reports no key, so probes stay empty.
func writeFakePi(t *testing.T, f *runFixture, fake string) {
	t.Helper()
	testshim.Script(t, filepath.Join(f.tmp, "bin"), "pi", "#!/bin/sh\ncase \"$*\" in *'auth print-api-key'*) if [ -n \"$FAKE_ZAI_KEY\" ]; then printf '%s\\n' \"$FAKE_ZAI_KEY\"; exit 0; fi; exit 1;; esac\nexec "+shellQuote(testshim.Bash(t))+" "+shellQuote(filepath.ToSlash(fake))+" \"$@\"\n")
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

func piContextLimitStep(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "agentio", "testdata", "pi-limit-zai.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	refusalMessage := "Weekly/Monthly Limit Exhausted."
	dataText := strings.Replace(string(data), `"content":[]`, `"content":[{"type":"text","text":"`+refusalMessage+`"}]`, 1)
	assistant, err := json.Marshal(map[string]any{"type": "message_end", "message": map[string]any{
		"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "I modified tracked.txt and confirmed the local work."}}, "stopReason": "stop",
	}})
	if err != nil {
		t.Fatal(err)
	}
	return "printf changed > tracked.txt\nprintf '%s\\n' " + shellQuote(`{"type":"session","id":"pi-session"}`) +
		"\nprintf '%s' " + shellQuote(string(assistant)+"\n"+dataText) + "\n"
}

func TestRunFallbackCarriesRefusedAttemptContext(t *testing.T) {
	f := fallbackExecutorRunFixture(t)
	f.write("tracked.txt", "before\n")
	f.commit("seed tracked file")
	f.steps(piContextLimitStep(t), codexFallbackSuccessStep(f))
	r := f.cli("run", "--no-wait", "--attempts", "1", "--max-units", "1")
	f.check(r, 0, "P001.1.1 done:", "switching to codex gpt-5.5")
	calls := f.calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want pi refusal then codex success\n%s", len(calls), r.out)
	}
	previous := calls[1].Stdin
	for _, want := range []string{"## Previous attempt", "### Working tree", "tracked.txt", "### Last messages", "I modified tracked.txt and confirmed the local work."} {
		if !strings.Contains(previous, want) {
			t.Fatalf("fallback prompt missing %q:\n%s", want, previous)
		}
	}
	if strings.Contains(previous, "Weekly/Monthly Limit Exhausted.") {
		t.Fatalf("fallback prompt contains the refusal message:\n%s", previous)
	}
}

func TestRunRoleFallbackOnLimit(t *testing.T) {
	f := fallbackExecutorRunFixture(t)
	f.replace(planFile, "fallback: codex | gpt-5.5 | -", "fallback: codex | gpt-6-luna | max\nheavy: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high")
	f.replace(planFile, "Commit: P001.1.1: first unit", "Role: heavy\nCommit: P001.1.1: first unit")
	f.steps(piLimitStep(t), codexFallbackSuccessStep(f))
	r := f.cli("run", "--no-wait", "--attempts", "1", "--max-units", "1")
	f.check(r, 0, "P001.1.1 done:", "switching to codex gpt-6.1-sol", "via codex gpt-6.1-sol")
	calls := f.calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want the pi refusal then the second heavy route\n%s", len(calls), r.out)
	}
	if !strings.Contains(strings.Join(calls[0].Argv, " "), "zai/glm-5.3") || !strings.Contains(strings.Join(calls[1].Argv, " "), "gpt-6.1-sol") {
		t.Fatalf("routes = %v", calls)
	}
	if strings.Contains(r.out+r.err, "gpt-6-luna") {
		t.Fatalf("the role unit used the plan fallback:\n%s%s", r.out, r.err)
	}
}

func TestRunExplicitExecutorKeepsFallback(t *testing.T) {
	f := fallbackExecutorRunFixture(t)
	f.replace(planFile, "fallback: codex | gpt-5.5 | -", "fallback: codex | gpt-6-luna | max\nheavy: claude | claude-haiku-5-5 | -; codex | gpt-6.1-sol | high")
	f.replace(planFile, "Commit: P001.1.1: first unit", "Executor: pi | zai/glm-5.3 | max\nCommit: P001.1.1: first unit")
	f.steps(piLimitStep(t), codexFallbackSuccessStep(f))
	r := f.cli("run", "--no-wait", "--attempts", "1", "--max-units", "1")
	f.check(r, 0, "P001.1.1 done:", "switching to codex gpt-6-luna", "via codex gpt-6-luna")
	calls := f.calls()
	if len(calls) != 2 || !strings.Contains(strings.Join(calls[1].Argv, " "), "gpt-6-luna") {
		t.Fatalf("calls = %+v\n%s", calls, r.out)
	}
	if strings.Contains(r.out+r.err, "gpt-6.1-sol") {
		t.Fatalf("the explicit Executor unit used a role chain:\n%s%s", r.out, r.err)
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

func TestRunQuotaSkipRoleChain(t *testing.T) {
	f := executorRunFixture(t)
	f.replace(planFile, "status: active", "status: active\nfallback: codex | gpt-5.5 | -\nheavy: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high")
	f.replace(planFile, "Commit: P001.1.1: first unit", "Role: heavy\nCommit: P001.1.1: first unit")
	future := time.Now().Add(2 * time.Hour).UTC().Format("Jan 2 at 3:04pm")
	usage := "Current session: 100% used · resets " + future + " (UTC)"
	fake, err := filepath.Abs("../agentio/testdata/fake_agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	testshim.Script(t, filepath.Join(f.tmp, "bin"), "claude", "#!/bin/sh\ncase \"$*\" in *'/usage'*) printf '%s\\n' "+shellQuote(usage)+"; exit 0;; esac\nexec "+shellQuote(testshim.Bash(t))+" "+shellQuote(filepath.ToSlash(fake))+" \"$@\"\n")
	f.steps(codexFallbackSuccessStep(f))
	r := f.cli("run", "--no-wait", "--attempts", "1", "--max-units", "1")
	f.check(r, 0, "claude: quota exhausted until", "via codex gpt-6.1-sol")
	calls := f.calls()
	if len(calls) != 1 || !strings.Contains(strings.Join(calls[0].Argv, " "), "gpt-6.1-sol") {
		t.Fatalf("calls = %+v\n%s", calls, r.out)
	}
}

func TestRunQuotaSkip(t *testing.T) {
	for _, mode := range []string{"probe", "fallback-probe", "stream"} {
		t.Run(mode, func(t *testing.T) {
			var f *runFixture
			if mode == "fallback-probe" {
				f = fallbackExecutorRunFixture(t)
				f.replace(planFile, "fallback: codex | gpt-5.5 | -", "fallback: claude | claude-opus-5-5 | high; codex | gpt-5.5 | -")
			} else {
				f = executorRunFixture(t)
				f.replace(planFile, "status: active", "status: active\nfallback: codex | gpt-5.5 | -")
			}
			args := []string{"--no-wait", "--attempts", "1"}
			var steps []string
			wantCalls := 1
			if mode == "probe" || mode == "fallback-probe" {
				future := time.Now().Add(2 * time.Hour).UTC().Format("Jan 2 at 3:04pm")
				usage := "Current session: 100% used · resets " + future + " (UTC)"
				fake, err := filepath.Abs("../agentio/testdata/fake_agent.sh")
				if err != nil {
					t.Fatal(err)
				}
				testshim.Script(t, filepath.Join(f.tmp, "bin"), "claude", "#!/bin/sh\ncase \"$*\" in *'/usage'*) printf '%s\\n' "+shellQuote(usage)+"; exit 0;; esac\nexec "+shellQuote(testshim.Bash(t))+" "+shellQuote(filepath.ToSlash(fake))+" \"$@\"\n")
				if mode == "fallback-probe" {
					piRefusal := strings.Replace(piLimitStep(t), "printf partial > partial.txt\n", "", 1)
					steps = []string{piRefusal, codexFallbackSuccessStep(f)}
					wantCalls = 2
				} else {
					steps = []string{codexFallbackSuccessStep(f)}
				}
				args = append(args, "--max-units", "1")
			} else {
				f.replace(planFile, "Executor: codex | gpt-5.5 | -\n", "")
				reset := int(time.Now().Add(2 * time.Hour).Unix())
				steps = []string{
					quotaEvent(1, .4, reset) + writeReply("a.txt"),
					strings.ReplaceAll(codexStep(f, 2, 20, 30, true), "u2.txt", "b.txt"),
				}
				args = append(args, "--max-units", "2")
				wantCalls = 2
			}
			f.steps(steps...)
			r := f.cli(append([]string{"run"}, args...)...)
			f.check(r, 0, "claude: quota exhausted until", "via codex gpt-5.5", "switch")
			calls := f.calls()
			if len(calls) != wantCalls {
				t.Fatalf("calls = %d, want %d\n%s", len(calls), wantCalls, r.out)
			}
			for i, call := range calls {
				if !strings.Contains(strings.Join(call.Argv, " "), "gpt-5.5") && (mode == "probe" || i == 1) {
					t.Fatalf("call %d did not use the fallback route: %+v", i, call.Argv)
				}
			}
			if mode == "stream" {
				probes, err := os.ReadFile(filepath.Join(f.tmp, "probes"))
				if err != nil {
					t.Fatal(err)
				}
				if got := strings.Count(strings.TrimSpace(string(probes)), "\n") + 1; got != 2 {
					t.Fatalf("quota was probed %d times, want one start probe per agent:\n%s", got, probes)
				}
			}
		})
	}
}

func TestReplanFallback(t *testing.T) {
	f := executorRunFixture(t)
	f.replace(planFile, "status: active", "status: active\nfallback: codex | gpt-6-luna | max\nreplan: pi | zai/glm-5.3 | max; codex | gpt-5.5 | -")
	fake, err := filepath.Abs("../agentio/testdata/fake_agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	writeFakePi(t, f, fake)
	piRefusal := strings.Replace(piLimitStep(t), "printf partial > partial.txt\n", "", 1)
	codexReplan := editPlan("### [!] P001.1.1", "### [~] P001.1.1") + codexStep(f, 3, 20, 30, false)
	f.steps(blockReply(), piRefusal, codexReplan, writeReply("a.txt"))
	r := f.cli("run", "--no-wait", "--attempts", "1", "--max-units", "1")
	f.check(r, 0, "replan", "switching to codex gpt-5.5", "1 replan, 1 switch", "run: 1 unit,")
	calls := f.calls()
	if len(calls) != 4 {
		t.Fatalf("calls = %d, want unit block, pi refusal, codex replan, and unit retry\n%s", len(calls), r.out)
	}
	if !strings.Contains(strings.Join(calls[1].Argv, " "), "zai/glm-5.3") || !strings.Contains(strings.Join(calls[2].Argv, " "), "gpt-5.5") || !strings.Contains(strings.Join(calls[3].Argv, " "), "claude-opus-5-5") {
		t.Fatalf("replan chain routes are wrong: %+v", calls)
	}
	if strings.Contains(strings.Join(calls[2].Argv, " "), "pi-session") || !strings.Contains(calls[2].Stdin, "# Replan P001.1.1") || !strings.Contains(calls[2].Stdin, "## Previous attempt") {
		t.Fatalf("fallback did not receive a fresh complete replan prompt: %+v", calls[2])
	}
	if strings.Contains(r.out+r.err, "gpt-6-luna") {
		t.Fatalf("replan used the plan fallback:\n%s%s", r.out, r.err)
	}
	var replanEvents []string
	for _, record := range fixtureEvents(t, f) {
		if record["replan"] == true || record["event"] == "replan" {
			replanEvents = append(replanEvents, record["event"].(string))
		}
		if record["event"] == "replan" && (record["result"] != "done" || record["route"].(map[string]any)["agent"] != "codex") {
			t.Fatalf("replan result = %v", record)
		}
	}
	if got := strings.Join(replanEvents, ","); got != "attempt,route_switch,attempt,commit,replan" {
		t.Fatalf("replan events = %s", got)
	}
}

func TestReplanChainExhaustedSkipsFallback(t *testing.T) {
	f := executorRunFixture(t)
	f.replace(planFile, "status: active", "status: active\nfallback: codex | gpt-6-luna | max\nreplan: pi | zai/glm-5.3 | max; codex | gpt-5.5 | -")
	fake, err := filepath.Abs("../agentio/testdata/fake_agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	writeFakePi(t, f, fake)
	piRefusal := strings.Replace(piLimitStep(t), "printf partial > partial.txt\n", "", 1)
	codexLimit := emit(map[string]any{"type": "thread.started", "thread_id": "t-lim"}) + emit(map[string]any{"type": "turn.failed", "error": map[string]any{"message": "usage limit reached"}})
	f.steps(blockReply(), piRefusal, codexLimit)
	r := f.cli("run", "--no-wait", "--attempts", "1", "--max-units", "1")
	f.check(r, 7, "all replan executors unavailable until", "the plan is restored")
	calls := f.calls()
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want the unit block and two refused replan routes\n%s", len(calls), r.out)
	}
	for _, call := range calls {
		if strings.Contains(strings.Join(call.Argv, " "), "gpt-6-luna") {
			t.Fatalf("replan fell back to the plan fallback route: %+v", call.Argv)
		}
	}
}
