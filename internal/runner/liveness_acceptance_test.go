package runner

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Acceptance of P045: each scenario runs `docflow run` with the fake agent and
// reads its config from DOCFLOW_AGENTS, the way a person configures the runner.

// acceptRun runs the unit through `docflow run` with the config found only
// through DOCFLOW_AGENTS.
func acceptRun(f *runFixture, steps []string, args ...string) runResult {
	f.t.Helper()
	f.t.Setenv("DOCFLOW_AGENTS", f.config)
	f.steps(steps...)
	return f.cli(append([]string{"run", "--no-wait", "--max-units", "1"}, args...)...)
}

func TestAcceptSilentAgentStopsAsStalled(t *testing.T) {
	f := newRunFixture(t)
	f.configure("stream = \"claude\"\nidle_timeout = 5\n")
	step := emit(map[string]any{"type": "system", "subtype": "init", "session_id": "s1"}) + "sleep 10\n"
	r := acceptRun(f, []string{step, writeReply("a.txt")}, "--attempts", "2")
	f.check(r, 0, "P001.1.1: attempt 1: agent silent for 5s, no tool running — stopped", "P001.1.1 done:")
	calls := f.calls()
	if len(calls) != 2 || !strings.Contains(strings.Join(calls[1].Argv, " "), "--resume s1") {
		t.Fatalf("attempt 2 did not resume the session: %v\n%s%s", calls, r.out, r.err)
	}
	if log := f.read(".git/docflow/run/P001.1.1.1.out"); !regexp.MustCompile(`--- exit -?\d+ \(stalled\)\n?$`).MatchString(log) {
		t.Errorf("the stopped call's log does not end as stalled:\n%s", log)
	}
}

func TestAcceptToolInFlightIsNotStalled(t *testing.T) {
	f := newRunFixture(t)
	f.configure("stream = \"claude\"\nidle_timeout = 5\n")
	step := emit(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{
		"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": "go test ./..."},
	}}}}) + "sleep 9\n" +
		emit(map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{
			"type": "tool_result", "tool_use_id": "t1", "content": "ok",
		}}}}) + writeReply("a.txt")
	r := acceptRun(f, []string{step}, "--attempts", "1")
	f.check(r, 0, "P001.1.1 done:")
	if text := r.out + r.err; strings.Contains(text, "silent") || strings.Contains(text, "stalled") {
		t.Errorf("a running tool was stopped as idle:\n%s", text)
	}
	if calls := f.calls(); len(calls) != 1 {
		t.Errorf("calls = %d, want the one that finished its tool", len(calls))
	}
}

func TestAcceptTimeCapWhileActiveResumes(t *testing.T) {
	f := newRunFixture(t)
	f.configure("stream = \"claude\"\ntimeout = 2\n")
	r := acceptRun(f, []string{activeStep(), writeReply("a.txt")}, "--attempts", "2")
	f.check(r, 0, "P001.1.1 done:")
	text := r.out + r.err
	if !regexp.MustCompile(`(?m)↻ attempt 1: time cap 2s reached while active \(\d+ turns?\); resuming the session$`).MatchString(text) {
		t.Errorf("no active-cap line:\n%s", text)
	}
	if strings.Contains(text, "timed out") {
		t.Errorf("the active attempt reads as a failure:\n%s", text)
	}
	if !strings.Contains(text, "hint: P001.1.1 needs more than 2s on fake -: split the unit or set timeout: in the plan header") {
		t.Errorf("the run summary carries no cap hint:\n%s", text)
	}
}

func TestAcceptAgentLogWrittenWhileRunning(t *testing.T) {
	f := newRunFixture(t)
	f.configure("stream = \"claude\"\n")
	marker := emit(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{
		"type": "text", "text": "streamed-marker",
	}}}})
	// The agent looks for its own stdout line in the runner's log before it
	// exits: the line is there only if the log is written while it runs.
	step := marker + `for i in 1 2 3 4 5 6 7 8 9 10; do grep -rqs '"text":"streamed-marker"' .git/docflow/run && break; sleep 0.5; done
if grep -rqs '"text":"streamed-marker"' .git/docflow/run; then
` + writeReply("a.txt") + "else\n" + envelope(map[string]any{"status": "blocked", "reason": "stdout is not in the log while the agent runs"}) + "fi\n"
	r := acceptRun(f, []string{step}, "--attempts", "1")
	f.check(r, 0, "P001.1.1 done:")
	b, err := os.ReadFile(filepath.Join(f.root, ".git/docflow/run/P001.1.1.1.out"))
	if err != nil {
		t.Fatal(err)
	}
	if log := string(b); !strings.Contains(log, "streamed-marker") || !strings.HasSuffix(strings.TrimRight(log, "\n"), "--- exit 0") {
		t.Errorf("the finished log lost the line or the exit row:\n%s", log)
	}
}
