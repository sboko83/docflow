package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The role acceptance tests write the plan header with the built CLI's
// roster apply from the P036 draft roster in $XDG_CONFIG_HOME, then run
// the plan with pi, codex and claude stubs in PATH.

// rosterRunFixture gives the first unit field1 and the second field2, then
// applies the draft roster: executor: pi | zai/glm-5.3-flash | max,
// fallback: codex | gpt-6-luna | max; claude | claude-sonnet-5-5 | high,
// heavy: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high.
func rosterRunFixture(t *testing.T, field1, field2 string) *runFixture {
	t.Helper()
	f := executorRunFixture(t)
	text := strings.Replace(f.read(planFile), "executor: claude | claude-opus-5-5 | high\n", "", 1)
	text = strings.Replace(text, "Executor: codex | gpt-5.5 | -\n", "", 1)
	text = strings.Replace(text, "Commit: P001.1.1:", field1+"\nCommit: P001.1.1:", 1)
	text = strings.Replace(text, "Commit: P001.1.2:", field2+"\nCommit: P001.1.2:", 1)
	f.write(planFile, text)
	f.commit("roles")
	fake, err := filepath.Abs("../agentio/testdata/fake_agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	writeFakePi(t, f, fake)
	draft, err := os.ReadFile(filepath.Join("..", "roster", "testdata", "draft.toml"))
	if err != nil {
		t.Fatal(err)
	}
	f.write(filepath.Join(f.tmp, "docflow", "roster.toml"), string(draft))
	f.check(f.cli("roster", "apply", "P001"), 0, "commit: P001: apply roster")
	for _, line := range []string{
		"executor: pi | zai/glm-5.3-flash | max\n",
		"fallback: codex | gpt-6-luna | max; claude | claude-sonnet-5-5 | high\n",
		"heavy: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high\n",
	} {
		if !strings.Contains(f.read(planFile), line) {
			t.Fatalf("roster apply did not write %q:\n%s", line, f.read(planFile))
		}
	}
	f.commit("apply roster")
	return f
}

func TestRoleAcceptanceHeavyLimitTakesSecondHeavyRoute(t *testing.T) {
	f := rosterRunFixture(t, "Role: heavy", "Executor: pi | zai/glm-5.3 | max")
	f.steps(piLimitStep(t), codexFallbackSuccessStep(f))
	r := f.cli("run", "--no-wait", "--attempts", "1", "--max-units", "1")
	f.check(r, 0, "P001.1.1 done:", "limit until", "switching to codex gpt-6.1-sol", "via codex gpt-6.1-sol")
	calls := f.calls()
	if len(calls) != 2 || !strings.Contains(strings.Join(calls[0].Argv, " "), "zai/glm-5.3") || !strings.Contains(strings.Join(calls[1].Argv, " "), "gpt-6.1-sol") {
		t.Fatalf("calls = %+v\n%s%s", calls, r.out, r.err)
	}
	if strings.Contains(r.out+r.err, "gpt-6-luna") {
		t.Fatalf("the heavy unit used fallback:\n%s%s", r.out, r.err)
	}
}

func TestRoleAcceptanceExecutorUnitTakesFallback(t *testing.T) {
	f := rosterRunFixture(t, "Executor: pi | zai/glm-5.3 | max", "Role: heavy")
	f.steps(piLimitStep(t), codexFallbackSuccessStep(f))
	r := f.cli("run", "--no-wait", "--attempts", "1", "--max-units", "1")
	f.check(r, 0, "P001.1.1 done:", "limit until", "switching to codex gpt-6-luna", "via codex gpt-6-luna")
	calls := f.calls()
	if len(calls) != 2 || !strings.Contains(strings.Join(calls[0].Argv, " "), "zai/glm-5.3") || !strings.Contains(strings.Join(calls[1].Argv, " "), "gpt-6-luna") {
		t.Fatalf("calls = %+v\n%s%s", calls, r.out, r.err)
	}
	if strings.Contains(r.out+r.err, "gpt-6.1-sol") {
		t.Fatalf("the Executor unit used the heavy chain:\n%s%s", r.out, r.err)
	}
}
