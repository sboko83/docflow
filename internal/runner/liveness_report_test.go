package runner

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/agents"
)

// stallStep speaks once with a session id, then goes silent: the idle watchdog
// must stop it, not wait for the time cap.
func stallStep() string {
	return emit(map[string]any{"type": "system", "subtype": "init", "session_id": "s1"}) + "sleep 60\n"
}

// activeStep speaks every second, so the stream stays fresh at the cap.
func activeStep() string {
	event := emit(map[string]any{"type": "assistant", "message": map[string]any{
		"content": []any{map[string]any{"type": "text", "text": "tick"}},
	}})
	return "for i in 1 2 3 4 5 6; do " + event + "sleep 1; done\nsleep 60\n"
}

// toolStep opens a tool that never finishes: the stream is silent afterwards,
// the tool is what keeps the call alive.
func toolStep() string {
	return emit(map[string]any{"type": "assistant", "message": map[string]any{
		"content": []any{map[string]any{
			"type": "tool_use", "id": "t1", "name": "Bash",
			"input": map[string]any{"command": "go test ./..."},
		}},
	}}) + "sleep 60\n"
}

func TestRunStalledAttemptResumesSession(t *testing.T) {
	f := newRunFixture(t)
	f.configure("idle_timeout = 5\n")
	steps := []string{stallStep(), writeReply("a.txt")}
	r := f.run(steps, "--attempts", "2", "--max-units", "1")
	f.check(r, 0,
		"P001.1.1: attempt 1: agent silent for 5s, no tool running — stopped",
		"P001.1.1 done:")
	calls := f.calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want the stalled one and the resumed one\n%s%s", len(calls), r.out, r.err)
	}
	if got := strings.Join(calls[1].Argv, " "); !strings.Contains(got, "--resume s1") {
		t.Errorf("the stalled attempt was resumed without the session: %s", got)
	}
	if !strings.Contains(calls[1].Stdin, "Runner: the previous attempt stalled: agent silent for 5s, no tool running — stopped") {
		t.Errorf("the resumed prompt does not name the stall:\n%s", calls[1].Stdin)
	}
}

// TestRunStalledAttemptIsNotRefusal: the runner stopped a silent call, so the
// attempt is not a provider failure. The killed agent's output quotes the
// profile's limit and login patterns — reading them would send the run to the
// fallback route and stop the run for a login the agent never missed.
func TestRunStalledAttemptIsNotRefusal(t *testing.T) {
	f := newRunFixture(t)
	fake, _ := filepath.Abs("../agentio/testdata/fake_agent.sh")
	probe := "stream = \"claude\"\nlimit = \"usage limit\"\nauth = \"not logged in\"\n"
	f.configure(probe + "idle_timeout = 5\n")
	f.configure("[agents.alt]\ncmd = [\"/bin/bash\", " + jsonString(fake) + "]\nmodel_args = [\"--model\", \"{model}\"]\nresume_args = [\"--resume\", \"{session}\"]\nresult = \"json:structured_output\"\ntext = \"json:result\"\nsession = \"json:session_id\"\n" + probe)
	f.replace(planFile, "status: active", "status: active\nfallback: alt | other-model | -")
	steps := []string{
		emit(map[string]any{"type": "result", "is_error": true, "result": "usage limit; not logged in", "session_id": "s1"}) +
			"sleep 60\n",
		writeReply("a.txt"),
	}
	r := f.run(steps, "--attempts", "2", "--max-units", "1")
	f.check(r, 0, "P001.1.1: attempt 1: agent silent for 5s, no tool running — stopped", "P001.1.1 done:")
	if text := r.out + r.err; strings.Contains(text, "is not logged in") || strings.Contains(text, "switching") {
		t.Errorf("a stopped call routed as a provider problem:\n%s", text)
	}
	calls := f.calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want the stopped one and the resumed one\n%s%s", len(calls), r.out, r.err)
	}
	if argv := strings.Join(calls[1].Argv, " "); !strings.Contains(argv, "--resume s1") || strings.Contains(argv, "other-model") {
		t.Errorf("the stopped attempt did not resume the same route: %s", argv)
	}
}

func TestRunTimeCapWhileActiveResumesAndHints(t *testing.T) {
	f := newRunFixture(t)
	f.configure("stream = \"claude\"\ntimeout = 2\n")
	steps := []string{activeStep(), writeReply("a.txt")}
	r := f.run(steps, "--attempts", "2", "--max-units", "1")
	f.check(r, 0, "P001.1.1 done:")
	text := r.out + r.err
	cap := regexp.MustCompile(`↻ attempt 1: time cap 2s reached while active \(\d+ turns\); resuming the session`)
	if !cap.MatchString(text) {
		t.Errorf("no active-cap line:\n%s", text)
	}
	if strings.Contains(text, "timed out after 2 s") {
		t.Errorf("an active attempt is reported as a problem:\n%s", text)
	}
	hint := regexp.MustCompile(`hint: P001\.1\.1 needs more than 2s on fake -: split the unit or set timeout: in the plan header`)
	if !hint.MatchString(text) {
		t.Errorf("the run summary carries no cap hint:\n%s", text)
	}
}

func TestRunTimeCapDuringToolNamesTheActivity(t *testing.T) {
	f := newRunFixture(t)
	f.configure("stream = \"claude\"\ntimeout = 2\n")
	steps := []string{toolStep(), writeReply("a.txt")}
	r := f.run(steps, "--attempts", "2", "--max-units", "1")
	f.check(r, 0, "P001.1.1 done:")
	text := r.out + r.err
	if !strings.Contains(text, "↻ attempt 1: time cap 2s reached during Bash go test ./... (1 turn); resuming the session") {
		t.Errorf("the active-cap line does not name the running tool:\n%s", text)
	}
}

func TestRunSilentTimeoutStaysAProblem(t *testing.T) {
	// An agent that printed nothing the whole call is silent, not active: the
	// cap is reported as the problem line and the summary gives no hint.
	f := newRunFixture(t)
	f.configure("timeout = 2\n")
	r := f.run([]string{"sleep 60\n"}, "--attempts", "1", "--max-units", "1")
	f.check(r, 7, "P001.1.1: attempt 1: timed out after 2 s")
	text := r.out + r.err
	if strings.Contains(text, "↻") || strings.Contains(text, "hint:") {
		t.Errorf("a silent agent reads as active work:\n%s", text)
	}
}

func TestCapProblemAndStallProblemText(t *testing.T) {
	live := agentio.NewLiveness("claude")
	live.Observe(`{"type":"assistant","message":{"content":[{"type":"text","text":"tick"}]}}`)
	state := live.State()
	if got := capProblem("attempt 2", 2*time.Hour, "", state); got != "↻ attempt 2: time cap 2h reached while active (1 turn); resuming the session" {
		t.Fatalf("capProblem = %q", got)
	}
	if got := capProblem("replan attempt 1", 90*time.Minute, "Bash go test", state); got != "↻ replan attempt 1: time cap 1h30m reached during Bash go test (1 turn); resuming the session" {
		t.Fatalf("capProblem with a tool = %q", got)
	}
	if got := stallProblem(20 * time.Minute); got != "agent silent for 20m, no tool running — stopped" {
		t.Fatalf("stallProblem = %q", got)
	}
	if got := capHint("P001.1.1", 45*time.Second, agents.Route{Agent: "codex", Model: "gpt-6.1-sol", Effort: "high"}); got != "P001.1.1 needs more than 45s on codex gpt-6.1-sol: split the unit or set timeout: in the plan header" {
		t.Fatalf("capHint = %q", got)
	}
}

func TestCapReportNamesOnlyAFlyingTool(t *testing.T) {
	now := time.Now()
	live := agentio.NewLiveness("claude")
	// A tool that has finished leaves the stream's activity behind: the line
	// must not claim the call is still "during" it.
	live.Observe(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test"}}]}}`)
	live.Observe(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1"}]}}`)
	if got := capReport("attempt 1", 2*time.Hour, live, "Bash go test", now); !strings.Contains(got, "reached while active (1 turn)") {
		t.Fatalf("a finished tool is named as running: %q", got)
	}
	flying := agentio.NewLiveness("claude")
	flying.Observe(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test"}}]}}`)
	if got := capReport("attempt 1", 2*time.Hour, flying, "Bash go test", now); !strings.Contains(got, "reached during Bash go test (1 turn)") {
		t.Fatalf("a running tool is not named: %q", got)
	}
	if got := capReport("attempt 1", 2*time.Hour, nil, "Bash go test", now); got != "" {
		t.Fatalf("a call without a tracker has no active report: %q", got)
	}
	silent := agentio.NewLiveness("pi")
	if got := capReport("attempt 1", time.Hour, silent, "", now.Add(time.Hour)); got != "" {
		t.Fatalf("the tracker's start is counted as an event, the call is active: %q", got)
	}
}

func TestActiveAt(t *testing.T) {
	now := time.Now()
	silent := agentio.LivenessState{LastLine: now.Add(-time.Hour)}
	if activeAt(silent, now) {
		t.Error("an agent that never spoke is not active")
	}
	recent := agentio.LivenessState{Lines: 3, LastLine: now.Add(-30 * time.Second)}
	if !activeAt(recent, now) {
		t.Error("an agent that spoke 30s ago is active")
	}
	stale := agentio.LivenessState{Lines: 3, LastLine: now.Add(-5 * time.Minute)}
	if activeAt(stale, now) {
		t.Error("an agent silent for 5 minutes is not active")
	}
	tool := agentio.LivenessState{Lines: 1, LastLine: now.Add(-time.Hour), Tools: 1, OldestTool: now.Add(-time.Hour)}
	if !activeAt(tool, now) {
		t.Error("a running tool keeps the call active")
	}
}

func TestLivenessLabel(t *testing.T) {
	if got := livenessLabel("P001.1.1 1m20s", nil); got != "P001.1.1 1m20s" {
		t.Fatalf("label without a tracker = %q", got)
	}
	live := agentio.NewLiveness("claude")
	live.Observe(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}`)
	live.Observe(`{"type":"assistant","message":{"content":[{"type":"text","text":"tick"}]}}`)
	got := livenessLabel("P001.1.1 1m20s", live)
	// 2 turns, an event that just arrived, one tool that just started.
	want := regexp.MustCompile(`^P001\.1\.1 1m20s · turn 2 · 0s ago · tool 0s$`)
	if !want.MatchString(got) {
		t.Fatalf("label = %q, want %s", got, want)
	}
	live.Observe(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1"}]}}`)
	quiet := livenessLabel("P001.1.1 1m20s", live)
	if strings.Contains(quiet, "tool") || !strings.HasSuffix(quiet, "· turn 2 · 0s ago") {
		t.Fatalf("label after the tool finished = %q", quiet)
	}
}
