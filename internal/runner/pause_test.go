package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/testshim"
)

// controlRunSelf routes the cycle's self-calls to the CLI built for the
// fixture, the way the installed docflow calls itself in a real run. `before`
// sees every call's arguments and may block, which is how a test stops the
// runner inside verify or commit.
func controlRunSelf(t *testing.T, f *runFixture, before func(args []string)) {
	t.Helper()
	previous := runnerSelf
	runnerSelf = func(root string, args ...string) cycle.Result {
		if before != nil {
			before(args)
		}
		command := exec.Command(f.binary, args...)
		command.Dir = root
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		err := command.Run()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else if err != nil {
			return cycle.Result{Code: 127, Stderr: err.Error()}
		}
		return cycle.Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
	}
	t.Cleanup(func() { runnerSelf = previous })
}

// gateOn blocks the self-call whose first argument is `command` until the
// returned release runs, and reports when the runner reached it.
func gateOn(t *testing.T, command string) (chan struct{}, func(), func([]string)) {
	t.Helper()
	reached, proceed := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	before := func(args []string) {
		if len(args) > 0 && args[0] == command {
			once.Do(func() { close(reached) })
			<-proceed
		}
	}
	release := func() { releaseOnce.Do(func() { close(proceed) }) }
	t.Cleanup(release)
	return reached, release, before
}

func startControlledRun(t *testing.T, f *runFixture, control *Control, args Args) {
	t.Helper()
	startControlledRunWith(t, f, control, context.Background(), args)
}

// startControlledRunWith runs under a parent context the test owns: canceling
// it is not a Pause, and the run must not read as one.
func startControlledRunWith(t *testing.T, f *runFixture, control *Control, parent context.Context, args Args) {
	t.Helper()
	if err := control.StartRun(parent, f.root, args, ""); err != nil {
		t.Fatal(err)
	}
	request := <-control.Requests()
	if request.Kind != "confirm_start" {
		t.Fatalf("request = %q, want confirm_start", request.Kind)
	}
	if err := request.Answer(Response{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
}

func awaitDone(t *testing.T, control *Control) {
	t.Helper()
	select {
	case <-control.Done():
	case <-time.After(30 * time.Second):
		t.Fatal("the run did not stop")
	}
}

func awaitMarker(t *testing.T, f *runFixture, path string, control *Control) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			var events []Event
			for {
				select {
				case event := <-control.Events():
					events = append(events, event)
				default:
					t.Fatalf("the fake agent did not start; state=%s logs=%s events=%#v",
						control.State(), fixtureCallLogs(t, f, "P001.1.1"), events)
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// fixtureCallLogs returns the call logs of a unit: the agent's own stdout and
// stderr, which is where a failed fake agent explains itself.
func fixtureCallLogs(t *testing.T, f *runFixture, uid string) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(f.root, ".git/docflow/run", uid+".*.out"))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, file := range files {
		data, _ := os.ReadFile(file)
		out.WriteString("=== " + filepath.Base(file) + "\n" + string(data) + "\n")
	}
	return out.String()
}

func awaitPaused(t *testing.T, control *Control) {
	t.Helper()
	select {
	case <-control.Done():
	case <-time.After(25 * time.Second):
		t.Fatal("Pause did not stop the run")
	}
	if got := control.State(); got != StatePaused {
		t.Fatalf("state = %s, want Paused", got)
	}
}

func TestPauseStopsAgentGroupAndWritesCheckpoint(t *testing.T) {
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "agent-started")
	pidFile := filepath.Join(f.tmp, "child.pid")
	// The marker follows the pid file: a Pause on the marker must find the
	// descendant already recorded, or a slow shell loses it to the kill.
	f.steps(emit(map[string]any{"type": "system", "subtype": "init", "session_id": "pause-session"}) + "sleep 60 &\necho $! > " + shellQuote(pidFile) + "\ntouch " + shellQuote(marker) + "\nwait\n")
	control := NewControl(32, 2)
	startControlledRun(t, f, control, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 2})
	awaitMarker(t, f, marker, control)
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)
	pidData, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		t.Fatal(err)
	}
	// $! is the shell's own pid (an MSYS pid under Git Bash), so the shell
	// answers whether it is alive.
	if exec.Command("sh", "-c", `kill -0 "$1" 2>/dev/null`, "sh", strconv.Itoa(pid)).Run() == nil {
		t.Fatalf("agent descendant %d survived Pause", pid)
	}
	cp, err := ReadCheckpoint(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if cp.Plan != "P001" || cp.Unit != "P001.1.1" || cp.Stage != "agent" || cp.Agent != "fake" || cp.Session != "pause-session" || cp.Calls != 1 || len(cp.Logs) != 1 {
		t.Fatalf("checkpoint does not describe paused call: %#v", cp)
	}
	if cp.Logs[0] != ".git/docflow/run/P001.1.1.1.out" {
		t.Fatalf("checkpoint log = %q, want the call log relative to the checkout", cp.Logs[0])
	}
	var states []map[string]any
	for _, record := range fixtureEvents(t, f) {
		if record["event"] == "state" {
			states = append(states, record)
		}
	}
	if len(states) != 1 || states[0]["state"] != "Paused" || states[0]["stage"] != "agent" || states[0]["unit"] != "P001.1.1" {
		t.Fatalf("pause state events = %v", states)
	}
	assertPausedJournals(t, f, "paused")
}

func TestPauseDuringVerifyFinishesCommitBoundary(t *testing.T) {
	f := newRunFixture(t)
	f.steps(writeReply("a.txt"))
	reached, release, before := gateOn(t, "verify")
	controlRunSelf(t, f, before)
	control := NewControl(32, 2)
	startControlledRun(t, f, control, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)})
	select {
	case <-reached:
	case <-time.After(15 * time.Second):
		t.Fatal("runner did not reach verify")
	}
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	release()
	awaitPaused(t, control)
	_, unit, err := cycle.FindUnit(f.root, "P001.1.1")
	if err != nil || unit.Status != "x" {
		t.Fatalf("unit status = %q, err=%v; verify/commit boundary was not completed", unit.Status, err)
	}
	cp, err := ReadCheckpoint(f.root)
	if err != nil || cp.Stage != "complete" || cp.Unit != unit.ID {
		t.Fatalf("checkpoint = %#v, err=%v", cp, err)
	}
	if states := pausedStateEvents(t, f, "complete"); len(states) != 1 {
		t.Fatalf("pause state events at the commit boundary = %v", states)
	}
	// The unit did finish: the pause closes the run, not the unit.
	assertPausedJournals(t, f, "done")
}

func TestPauseCancelsQuotaWait(t *testing.T) {
	f := newRunFixture(t)
	control := NewControl(1, 1)
	started := make(chan struct{})
	if err := control.StartCheckout(context.Background(), f.root, func(ctx context.Context, host *Control) error {
		if !host.MarkRunning() {
			return errors.New("could not enter Running")
		}
		close(started)
		p := newProgress(nil, nil, false)
		err := waitForExecutorChain(ctx, p, "quota wait", time.Now().Add(time.Hour))
		if errors.Is(err, context.Canceled) && host.State() == StatePausing {
			ctx = context.WithValue(ctx, controlContextKey{}, host)
			_, err := writePauseCheckpoint(ctx, f.root, "P001", "P001.1.1", "quota", "", agents.Route{Agent: "fake"}, "", 0, 0, 0, nil)
			return err
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-control.Done():
	case <-time.After(time.Second):
		t.Fatal("Pause did not interrupt quota wait")
	}
	if got := control.State(); got != StatePaused {
		t.Fatalf("state = %s, want Paused", got)
	}
	cp, err := ReadCheckpoint(f.root)
	if err != nil || cp.Stage != "quota" || cp.Unit != "P001.1.1" {
		t.Fatalf("checkpoint = %#v, err=%v", cp, err)
	}
}

// The call Pause cancels has no result JSON and its truncated stream reads
// like a rejected provider limit. The runner classifies the cancellation
// before that handling: no retry, no route marked unavailable, no fallback
// call, no attempt consumed.
func TestPauseCancelsProviderCallWithoutRetryOrFallback(t *testing.T) {
	f := executorRunFixture(t)
	f.replace(planFile, "status: active", "status: active\nfallback: codex | gpt-5.5 | -")
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "claude-started")
	reset := time.Now().Add(2 * time.Hour).Unix()
	f.steps(emit(map[string]any{"type": "system", "subtype": "init", "session_id": "pause-claude", "model": "claude-opus-5-5"}) +
		emit(map[string]any{"type": "assistant", "message": map[string]any{
			"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "working"}}}}) +
		emit(map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{"status": "rejected", "resetsAt": reset}}) +
		"touch " + shellQuote(marker) + "\n" +
		"trap 'exit 143' TERM\nsleep 60 &\nwait\n")
	control := NewControl(64, 2)
	startControlledRun(t, f, control, Args{Attempts: 3})
	awaitMarker(t, f, marker, control)
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)
	calls := f.calls()
	if len(calls) != 1 {
		argv := make([]string, 0, len(calls))
		for _, call := range calls {
			argv = append(argv, strings.Join(call.Argv, " "))
		}
		t.Fatalf("agent calls = %d, want only the paused executor call: %v", len(calls), argv)
	}
	if argv := strings.Join(calls[0].Argv, " "); !strings.Contains(argv, "claude-opus-5-5") {
		t.Fatalf("the paused call is not the plan executor: %s", argv)
	}
	attempts, switches := 0, 0
	for _, record := range fixtureEvents(t, f) {
		switch record["event"] {
		case "attempt":
			attempts++
			if record["n"] != float64(1) {
				t.Fatalf("attempt event n = %v, want 1", record["n"])
			}
		case "route_switch":
			switches++
		}
	}
	if attempts != 1 || switches != 0 {
		t.Fatalf("attempts = %d, route switches = %d, want 1 and 0", attempts, switches)
	}
	if _, unit, err := cycle.FindUnit(f.root, "P001.1.1"); err != nil || unit.Status != "~" {
		t.Fatalf("unit status = %q, err = %v; Pause must leave the unit in progress", unit.Status, err)
	}
	cp, err := ReadCheckpoint(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if cp.Plan != "P001" || cp.Unit != "P001.1.1" || cp.Stage != "agent" || cp.Agent != "claude" ||
		cp.Model != "claude-opus-5-5" || cp.Effort != "high" || cp.Session != "pause-claude" ||
		cp.Calls != 1 || cp.Counts["calls"] != 1 || len(cp.Logs) != 1 || cp.Head == "" || cp.PlanHash == "" {
		t.Fatalf("checkpoint does not describe the paused call: %#v", cp)
	}
}

// Pause arriving while the commit runs reaches the stable boundary: the unit
// is [x], the unit's commit exists, and the checkpoint says complete.
func TestPauseAtCommitBoundaryCompletesUnit(t *testing.T) {
	f := newRunFixture(t)
	f.steps(writeReply("a.txt"))
	reached, release, before := gateOn(t, "commit")
	controlRunSelf(t, f, before)
	control := NewControl(64, 2)
	startControlledRun(t, f, control, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)})
	select {
	case <-reached:
	case <-time.After(15 * time.Second):
		t.Fatal("the runner did not reach the commit of the unit")
	}
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	release()
	awaitPaused(t, control)
	_, unit, err := cycle.FindUnit(f.root, "P001.1.1")
	if err != nil || unit.Status != "x" {
		t.Fatalf("unit status = %q, err = %v; the commit boundary did not complete", unit.Status, err)
	}
	if subject := f.git("log", "-1", "--format=%s"); !strings.Contains(subject, "P001.1.1") {
		t.Fatalf("HEAD commit = %q, want the unit's commit", subject)
	}
	cp, err := ReadCheckpoint(f.root)
	if err != nil || cp.Stage != "complete" || cp.Unit != unit.ID || cp.Calls != 1 {
		t.Fatalf("checkpoint = %#v, err = %v", cp, err)
	}
	if states := pausedStateEvents(t, f, "complete"); len(states) != 1 {
		t.Fatalf("pause state events at the commit boundary = %v", states)
	}
	assertPausedJournals(t, f, "done")
}

func intPointer(value int) *int { return &value }

func awaitFinished(t *testing.T, control *Control) {
	t.Helper()
	select {
	case <-control.Done():
	case <-time.After(30 * time.Second):
		t.Fatal("the resumed run did not finish")
	}
	if got := control.State(); got != StateFinished {
		t.Fatalf("state = %s, want Finished", got)
	}
}

// A pause taken before the agent wrote any result line has no session id. The
// resume starts exactly one new session with the full prompt and the recovery
// reason, never a fallback route.
func TestResumeWithoutSessionStartsOneNewSession(t *testing.T) {
	skipSlow(t)
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "agent-started")
	f.steps("touch " + shellQuote(marker) + "\nsleep 60 &\nwait\n")
	args := Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 2, MaxUnits: intPointer(1)}
	control := NewControl(64, 2)
	startControlledRun(t, f, control, args)
	awaitMarker(t, f, marker, control)
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)
	cp, err := ReadCheckpoint(f.root)
	if err != nil || cp.Session != "" || cp.Stage != "agent" || cp.Calls != 1 {
		t.Fatalf("checkpoint = %#v, err = %v; want a paused first call without a session", cp, err)
	}
	f.steps(writeReply("a.txt"))
	if err := control.ResumeRun(context.Background(), f.root, args, ""); err != nil {
		t.Fatal(err)
	}
	awaitFinished(t, control)
	calls := f.calls()
	if len(calls) != 1 {
		t.Fatalf("agent calls = %d, want one resumed call", len(calls))
	}
	if argv := strings.Join(calls[0].Argv, " "); strings.Contains(argv, "--resume") {
		t.Fatalf("a session without an id was resumed anyway: %s", argv)
	}
	for _, text := range []string{"## Recovery", "no session id", "Working tree"} {
		if !strings.Contains(calls[0].Stdin, text) {
			t.Fatalf("new session prompt lacks %q:\n%s", text, calls[0].Stdin)
		}
	}
	if _, unit, err := cycle.FindUnit(f.root, "P001.1.1"); err != nil || unit.Status != "x" {
		t.Fatalf("unit status = %q, err = %v", unit.Status, err)
	}
}

// A provider that refuses the saved session is retried once in a new session,
// inside the same attempt: the attempt limit is not reset and no fallback runs.
func TestResumeRejectedSessionStartsExactlyOneNewSession(t *testing.T) {
	skipSlow(t)
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "agent-started")
	f.steps(emit(map[string]any{"type": "system", "subtype": "init", "session_id": "paused-session"}) +
		"touch " + shellQuote(marker) + "\n" +
		"sleep 60 &\nwait\n")
	args := Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)}
	control := NewControl(64, 2)
	startControlledRun(t, f, control, args)
	awaitMarker(t, f, marker, control)
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)
	cp, err := ReadCheckpoint(f.root)
	if err != nil || cp.Session != "paused-session" {
		t.Fatalf("checkpoint = %#v, err = %v", cp, err)
	}
	f.steps(
		"printf 'error: session paused-session not found\\n' >&2\nexit 1\n",
		writeReply("a.txt"),
	)
	if err := control.ResumeRun(context.Background(), f.root, args, ""); err != nil {
		t.Fatal(err)
	}
	awaitFinished(t, control)
	calls := f.calls()
	if len(calls) != 2 {
		t.Fatalf("agent calls = %d, want the rejected resume and one new session", len(calls))
	}
	if argv := strings.Join(calls[0].Argv, " "); !strings.Contains(argv, "--resume paused-session") {
		t.Fatalf("the first resumed call did not use the saved session: %s", argv)
	}
	if argv := strings.Join(calls[1].Argv, " "); strings.Contains(argv, "--resume") {
		t.Fatalf("the rejected session was resumed twice: %s", argv)
	}
	for _, text := range []string{"## Recovery", "rejected the resumed session"} {
		if !strings.Contains(calls[1].Stdin, text) {
			t.Fatalf("replacement session prompt lacks %q:\n%s", text, calls[1].Stdin)
		}
	}
	if _, unit, err := cycle.FindUnit(f.root, "P001.1.1"); err != nil || unit.Status != "x" {
		t.Fatalf("unit status = %q, err = %v; a rejected resume must not spend the only attempt", unit.Status, err)
	}
}

// Pause then Resume can repeat: the second resume still continues the same
// attempt and session, and the unit finishes normally.
func TestRepeatedPauseAndResumeContinueTheSameAttempt(t *testing.T) {
	skipSlow(t)
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	first := filepath.Join(f.tmp, "first-started")
	second := filepath.Join(f.tmp, "second-started")
	kickoff := emit(map[string]any{"type": "system", "subtype": "init", "session_id": "pause-session"})
	f.steps(kickoff + "touch " + shellQuote(first) + "\nsleep 60 &\nwait\n")
	args := Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 2, MaxUnits: intPointer(1)}
	control := NewControl(64, 2)
	startControlledRun(t, f, control, args)
	awaitMarker(t, f, first, control)
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)
	// The session line is written before the marker: a Pause right after the
	// marker must find it in the agent's output.
	f.steps(emit(map[string]any{"type": "system", "subtype": "init", "session_id": "pause-session-2"}) +
		"touch " + shellQuote(second) + "\nsleep 60 &\nwait\n")
	if err := control.ResumeRun(context.Background(), f.root, args, ""); err != nil {
		t.Fatal(err)
	}
	awaitMarker(t, f, second, control)
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)
	cp, err := ReadCheckpoint(f.root)
	if err != nil || cp.Session != "pause-session-2" || cp.Calls != 1 {
		t.Fatalf("checkpoint after the second pause = %#v, err = %v; the attempt or session was reset", cp, err)
	}
	f.steps(writeReply("a.txt"))
	if err := control.ResumeRun(context.Background(), f.root, args, ""); err != nil {
		t.Fatal(err)
	}
	awaitFinished(t, control)
	calls := f.calls()
	if len(calls) != 1 {
		t.Fatalf("agent calls = %d, want one final call", len(calls))
	}
	if argv := strings.Join(calls[0].Argv, " "); !strings.Contains(argv, "--resume pause-session-2") {
		t.Fatalf("the second resume did not continue the saved session: %s", argv)
	}
	if _, unit, err := cycle.FindUnit(f.root, "P001.1.1"); err != nil || unit.Status != "x" {
		t.Fatalf("unit status = %q, err = %v", unit.Status, err)
	}
}

// The time spent Paused is not charged to the agent call: the resumed call gets
// the timeout left at the pause, and a wait as long as the whole timeout does
// not kill it.
func TestResumeExcludesPauseTimeFromTimeout(t *testing.T) {
	skipSlow(t)
	f := newRunFixture(t)
	f.replace(planFile, "created: 2026-10-12\n", "created: 2026-10-12\ntimeout: 4\n")
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "agent-started")
	f.steps("sleep 0.5\ntouch " + shellQuote(marker) + "\nsleep 60 &\nwait\n")
	args := Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)}
	control := NewControl(64, 2)
	startControlledRun(t, f, control, args)
	awaitMarker(t, f, marker, control)
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)
	cp, err := ReadCheckpoint(f.root)
	if err != nil || cp.Remaining <= 0 || cp.Remaining >= 4 {
		t.Fatalf("checkpoint remaining timeout = %v, err = %v", cp.Remaining, err)
	}
	time.Sleep(3 * time.Second)
	f.steps("sleep 2.5\n" + writeReply("a.txt"))
	if err := control.ResumeRun(context.Background(), f.root, args, ""); err != nil {
		t.Fatal(err)
	}
	awaitFinished(t, control)
	if _, unit, err := cycle.FindUnit(f.root, "P001.1.1"); err != nil || unit.Status != "x" {
		t.Fatalf("unit status = %q, err = %v; the pause was charged to the timeout", unit.Status, err)
	}
}

// A checkpoint taken after the unit's commit resumes at the next unit: the
// finished commit is not repeated and the agent is not called for it again.
func TestResumeDoesNotRepeatACompletedCommit(t *testing.T) {
	skipSlow(t)
	f := newRunFixture(t)
	f.steps(writeReply("a.txt"))
	reached, release, before := gateOn(t, "commit")
	controlRunSelf(t, f, before)
	args := Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(2)}
	control := NewControl(64, 2)
	startControlledRun(t, f, control, args)
	select {
	case <-reached:
	case <-time.After(15 * time.Second):
		t.Fatal("the runner did not reach the first commit")
	}
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	release()
	awaitPaused(t, control)
	cp, err := ReadCheckpoint(f.root)
	if err != nil || cp.Stage != "complete" || cp.Unit != "P001.1.1" {
		t.Fatalf("checkpoint = %#v, err = %v", cp, err)
	}
	subject := "P001.1.1: first unit"
	beforeLog := f.git("log", "--format=%s")
	f.steps(writeReply("b.txt"))
	if err := control.ResumeRun(context.Background(), f.root, args, ""); err != nil {
		t.Fatal(err)
	}
	awaitFinished(t, control)
	afterLog := f.git("log", "--format=%s")
	if strings.Count(afterLog, subject) != 1 {
		t.Fatalf("the completed commit was repeated:\nbefore:\n%s\nafter:\n%s", beforeLog, afterLog)
	}
	if !strings.Contains(afterLog, "P001.1.2: second unit") {
		t.Fatalf("the resumed run did not continue to the next unit:\n%s", afterLog)
	}
	calls := f.calls()
	if len(calls) != 1 || !strings.Contains(calls[0].Stdin, "P001.1.2") {
		t.Fatalf("agent calls after resume = %d, want only the next unit", len(calls))
	}
}

// A moved HEAD or an edited plan is a visible recovery conflict, not a silent
// continuation of the wrong work.
func TestResumeReportsPlanAndHeadConflicts(t *testing.T) {
	skipSlow(t)
	for _, tc := range []struct {
		name   string
		change func(*runFixture)
	}{
		{"plan", func(f *runFixture) { f.replace(planFile, "title: Plan", "title: Renamed") }},
		{"head", func(f *runFixture) { f.write("unrelated.txt", "x"); f.commit("unrelated") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRunFixture(t)
			controlRunSelf(t, f, nil)
			marker := filepath.Join(f.tmp, "agent-started")
			f.steps("touch " + shellQuote(marker) + "\nsleep 60 &\nwait\n")
			args := Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)}
			control := NewControl(64, 2)
			startControlledRun(t, f, control, args)
			awaitMarker(t, f, marker, control)
			if err := control.Pause(); err != nil {
				t.Fatal(err)
			}
			awaitPaused(t, control)
			tc.change(f)
			err := control.ResumeRun(context.Background(), f.root, args, "")
			var conflict RecoveryConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("ResumeRun error = %v, want RecoveryConflictError", err)
			}
			if got := control.State(); got != StatePaused {
				t.Fatalf("state = %s, want Paused after a conflict", got)
			}
			select {
			case event := <-control.Events():
				for event.Type != "recovery_conflict" && event.Type != "" {
					event = <-control.Events()
				}
			default:
			}
		})
	}
}

// Stop after unit is a controller command: the running unit reaches its commit
// boundary and the next unit never starts.
func TestStopAfterUnitFinishesTheCurrentUnitOnly(t *testing.T) {
	skipSlow(t)
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "agent-started")
	f.steps("touch " + shellQuote(marker) + "\nsleep 0.5\n" + writeReply("a.txt"))
	control := NewControl(64, 2)
	startControlledRun(t, f, control, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1})
	awaitMarker(t, f, marker, control)
	if err := control.StopAfterUnit(); err != nil {
		t.Fatal(err)
	}
	awaitFinished(t, control)
	log := f.git("log", "--format=%s")
	if !strings.Contains(log, "P001.1.1: first unit") {
		t.Fatalf("the current unit did not finish:\n%s", log)
	}
	if strings.Contains(log, "P001.1.2: second unit") {
		t.Fatalf("a new unit started after Stop after unit:\n%s", log)
	}
	// Stop after unit is not a stop of the work: the unit is done and the
	// run_end keeps the normal finish, with no stopped or paused marker.
	for _, file := range eventFiles(t, f) {
		for _, record := range readEvents(t, file) {
			switch record["event"] {
			case "unit_end":
				if record["status"] != "done" {
					t.Fatalf("stop-after-unit unit_end = %v, want status done", record)
				}
			case "run_end":
				if record["exit_code"] != float64(0) || record["reason"] == "stopped" {
					t.Fatalf("stop-after-unit run_end = %v, want a normal finish", record)
				}
				if _, ok := record["paused"]; ok {
					t.Fatalf("stop-after-unit run_end carries a pause field: %v", record)
				}
			}
		}
	}
}

// `docflow run --stop` from another process reaches a live controlled run
// through its checkout lock: the current unit finishes and the next one never
// starts, as with the workspace's own Stop after unit.
func TestRunStopFlagStopsAControlledRun(t *testing.T) {
	skipSlow(t)
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "agent-started")
	release := filepath.Join(f.tmp, "agent-release")
	f.steps("touch " + shellQuote(marker) + "\nwhile [ ! -f " + shellQuote(release) + " ]; do sleep 0.1; done\n" + writeReply("a.txt"))
	control := NewControl(64, 2)
	startControlledRun(t, f, control, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1})
	awaitMarker(t, f, marker, control)
	r := f.cli("run", "--stop")
	f.check(r, 0)
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	awaitFinished(t, control)
	log := f.git("log", "--format=%s")
	if !strings.Contains(log, "P001.1.1: first unit") || strings.Contains(log, "P001.1.2: second unit") {
		t.Fatalf("run --stop did not stop after the current unit:\n%s", log)
	}
	if calls := f.calls(); len(calls) != 1 {
		t.Fatalf("agent calls = %d, want the current unit only", len(calls))
	}
}

// A Stop after unit accepted while the execution is still starting must
// survive the run's own start-up: the current unit finishes and the next one
// never starts. The run's start-up used to remove the request.
func TestStopAfterUnitAtRunStartStopsAfterTheFirstUnit(t *testing.T) {
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "agent-started")
	f.steps("touch " + shellQuote(marker) + "\n" + writeReply("a.txt"))
	args := Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1}
	control := NewControl(64, 2)
	if err := control.StartCheckout(context.Background(), f.root, func(ctx context.Context, host *Control) error {
		if !host.MarkRunning() {
			return errors.New("the host did not enter Running")
		}
		if err := host.StopAfterUnit(); err != nil {
			return err
		}
		code, err := runControlled(ctx, f.root, args, "", host, nil, true)
		if err != nil {
			return err
		}
		if code != 0 {
			return errors.New("run exited with code " + strconv.Itoa(code))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	awaitDone(t, control)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the current unit did not run: %v", err)
	}
	log := f.git("log", "--format=%s")
	if !strings.Contains(log, "P001.1.1: first unit") {
		t.Fatalf("the current unit did not finish:\n%s", log)
	}
	for _, file := range eventFiles(t, f) {
		for _, record := range readEvents(t, file) {
			if record["event"] == "unit_start" && record["unit"] == "P001.1.2" {
				t.Fatalf("the stop request was lost at start-up:\n%s", log)
			}
		}
	}
}

// A stop request left by an earlier run does not block the next interactive
// start: the host clears it before the owner can ask to stop again.
func TestInteractiveStartClearsAStaleStopRequest(t *testing.T) {
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "agent-started")
	f.steps("touch " + shellQuote(marker) + "\n" + writeReply("a.txt"))
	stop, err := stopFile(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(stop), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stop, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	control := NewControl(64, 2)
	startControlledRun(t, f, control, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)})
	awaitMarker(t, f, marker, control)
	awaitFinished(t, control)
}

// A checkpoint survives the process: a fresh controller (Idle, as after
// reopening the application) resumes the paused unit.
func TestResumeFromAFreshController(t *testing.T) {
	skipSlow(t)
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "agent-started")
	f.steps(emit(map[string]any{"type": "system", "subtype": "init", "session_id": "reopen-session"}) +
		"touch " + shellQuote(marker) + "\n" +
		"sleep 60 &\nwait\n")
	args := Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)}
	control := NewControl(64, 2)
	startControlledRun(t, f, control, args)
	awaitMarker(t, f, marker, control)
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)
	f.steps(writeReply("a.txt"))
	reopened := NewControl(64, 2)
	if got := reopened.State(); got != StateIdle {
		t.Fatalf("fresh controller state = %s, want Idle", got)
	}
	if err := reopened.ResumeRun(context.Background(), f.root, args, ""); err != nil {
		t.Fatal(err)
	}
	awaitFinished(t, reopened)
	calls := f.calls()
	if len(calls) != 1 {
		t.Fatalf("agent calls = %d, want one resumed call", len(calls))
	}
	if argv := strings.Join(calls[0].Argv, " "); !strings.Contains(argv, "--resume reopen-session") {
		t.Fatalf("the reopened controller did not continue the saved session: %s", argv)
	}
	if _, unit, err := cycle.FindUnit(f.root, "P001.1.1"); err != nil || unit.Status != "x" {
		t.Fatalf("unit status = %q, err = %v", unit.Status, err)
	}
}

// awaitEvent waits for the controller to publish an event of a type. The
// quota probe runs after the loop's cancellation check, so the event marks the
// point from which the unit's executor wait is the only thing left to do.
func awaitEvent(t *testing.T, control *Control, typ string) Event {
	t.Helper()
	deadline := time.After(20 * time.Second)
	for {
		select {
		case event := <-control.Events():
			if event.Type == typ {
				return event
			}
		case <-deadline:
			t.Fatalf("the controller published no %q event", typ)
		}
	}
}

func pausedStateEvents(t *testing.T, f *runFixture, stage string) []map[string]any {
	t.Helper()
	var states []map[string]any
	for _, file := range eventFiles(t, f) {
		for _, record := range readEvents(t, file) {
			if record["event"] == "state" && record["state"] == "Paused" && record["stage"] == stage {
				states = append(states, record)
			}
		}
	}
	return states
}

// eventFiles lists every run stream of the checkout: a resume opens a second
// one, so a fixture-wide read cannot rely on exactly one file.
func eventFiles(t *testing.T, f *runFixture) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(f.root, ".git/docflow/run/*.events.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("event files = %v, err = %v", files, err)
	}
	return files
}

// assertPausedJournals checks how a paused run closes both levels of the
// stream: the paused unit is an event of its own rather than a runner failure,
// and the run_end carries the pause with the success code.
func assertPausedJournals(t *testing.T, f *runFixture, unitStatus string) {
	t.Helper()
	units, runs := 0, 0
	for _, file := range eventFiles(t, f) {
		for _, record := range readEvents(t, file) {
			switch record["event"] {
			case "unit_end":
				units++
				if record["status"] != unitStatus {
					t.Fatalf("unit_end = %v, want status %q", record, unitStatus)
				}
			case "run_end":
				runs++
				if record["paused"] != true || record["reason"] != "paused" || record["exit_code"] != float64(0) {
					t.Fatalf("paused run_end = %v, want paused true, reason paused, exit_code 0", record)
				}
			}
		}
	}
	if units != 1 || runs != 1 {
		t.Fatalf("unit_end events = %d, run_end events = %d, want one of each", units, runs)
	}
}

// assertStoppedJournals checks how a controlled Stop now closes both levels of
// the stream: the interrupted unit is stopped rather than failed, and the
// run_end is the action with the success code and no pause field.
func assertStoppedJournals(t *testing.T, f *runFixture) {
	t.Helper()
	units, runs := 0, 0
	for _, file := range eventFiles(t, f) {
		for _, record := range readEvents(t, file) {
			switch record["event"] {
			case "unit_end":
				units++
				if record["status"] != "stopped" {
					t.Fatalf("unit_end = %v, want status stopped", record)
				}
			case "run_end":
				runs++
				if record["reason"] != "stopped" || record["exit_code"] != float64(0) {
					t.Fatalf("stopped run_end = %v, want reason stopped, exit_code 0", record)
				}
				if _, ok := record["paused"]; ok {
					t.Fatalf("stopped run_end carries a pause field: %v", record)
				}
			}
		}
	}
	if units != 1 || runs != 1 {
		t.Fatalf("unit_end events = %d, run_end events = %d, want one of each", units, runs)
	}
}

// A controlled Stop now is journaled as its own outcome: the host reports
// Finished, so neither level of the stream reads as a runner failure.
func TestStopNowJournalsAsAction(t *testing.T) {
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "agent-started")
	f.steps(emit(map[string]any{"type": "system", "subtype": "init", "session_id": "stop-session"}) + "touch " + shellQuote(marker) + "\nsleep 60 &\nwait\n")
	control := NewControl(32, 2)
	startControlledRun(t, f, control, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 2})
	awaitMarker(t, f, marker, control)
	if err := control.StopNow(); err != nil {
		t.Fatal(err)
	}
	awaitFinished(t, control)
	assertStoppedJournals(t, f)
	if _, err := ReadCheckpoint(f.root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a Stop now wrote a checkpoint: %v", err)
	}
}

// Without a control host the same cancellation stays a runner failure: a
// canceled parent must not be reclassified as a managed stop.
func TestCanceledParentDuringAgentCallJournalsAnError(t *testing.T) {
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	f.steps("sleep 60 &\nwait\n")
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	var code int
	var runErr error
	go func() {
		defer close(done)
		code, runErr = runWithOptions(f.root, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)}, io.Discard, io.Discard, "", runOptions{ctx: parent})
	}()
	awaitRecord(t, f, "attempt")
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the canceled run did not stop")
	}
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("canceled run error = %v, want context.Canceled", runErr)
	}
	units, runs := 0, 0
	for _, file := range eventFiles(t, f) {
		for _, record := range readEvents(t, file) {
			switch record["event"] {
			case "unit_end":
				units++
				if record["status"] != "error" {
					t.Fatalf("canceled unit_end = %v, want status error", record)
				}
			case "run_end":
				runs++
				if record["exit_code"] != float64(2) || record["reason"] != "context canceled" {
					t.Fatalf("canceled run_end = %v, want reason context canceled, exit_code 2", record)
				}
			}
		}
	}
	if units != 1 || runs != 1 {
		t.Fatalf("unit_end events = %d, run_end events = %d, want one of each", units, runs)
	}
	if code != 7 {
		t.Fatalf("canceled run code = %d, want 7", code)
	}
}

// A Pause ending a rejected resume is classified before the rejected-resume
// retry: the canceled call is the only start of the resumed run, and the
// runner never opens a replacement session with an already canceled context.
func TestPauseBeforeRejectedResumeHandlingStartsNoNewSession(t *testing.T) {
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "agent-started")
	f.steps(emit(map[string]any{"type": "system", "subtype": "init", "session_id": "paused-session"}) +
		"touch " + shellQuote(marker) + "\n" +
		"sleep 60 &\nwait\n")
	args := Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)}
	control := NewControl(64, 2)
	startControlledRun(t, f, control, args)
	awaitMarker(t, f, marker, control)
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)
	cp, err := ReadCheckpoint(f.root)
	if err != nil || cp.Session != "paused-session" || cp.Stage != "agent" {
		t.Fatalf("checkpoint = %#v, err = %v", cp, err)
	}
	rejected := filepath.Join(f.tmp, "rejected-started")
	f.steps("touch " + shellQuote(rejected) + "\n" +
		"printf 'error: session paused-session not found\\n' >&2\n" +
		"sleep 60 &\nwait\n")
	if err := control.ResumeRun(context.Background(), f.root, args, ""); err != nil {
		t.Fatal(err)
	}
	awaitMarker(t, f, rejected, control)
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)
	calls := f.calls()
	if len(calls) != 1 {
		argv := make([]string, 0, len(calls))
		for _, call := range calls {
			argv = append(argv, strings.Join(call.Argv, " "))
		}
		t.Fatalf("agent calls = %d, want only the paused resume: %v", len(calls), argv)
	}
	if argv := strings.Join(calls[0].Argv, " "); !strings.Contains(argv, "--resume paused-session") {
		t.Fatalf("the paused call did not resume the saved session: %s", argv)
	}
	if len(pausedStateEvents(t, f, "agent")) == 0 {
		t.Fatal("the paused resume published no state event")
	}
}

// Pause while every executor waits for its reset writes the quota checkpoint
// and publishes the same state, so a reopened application sees the wait.
func TestPauseDuringExecutorChainWait(t *testing.T) {
	f := executorRunFixture(t)
	reset := (time.Now().Unix()/60 + 120) * 60
	usage := "Current session: 100% used · resets " + time.Unix(reset, 0).UTC().Format("Jan 2 at 3:04pm") + " (UTC)"
	fake, err := filepath.Abs("../agentio/testdata/fake_agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	testshim.Script(t, filepath.Join(f.tmp, "bin"), "claude", "#!/bin/sh\ncase \"$*\" in *'/usage'*) printf '%s\\n' "+shellQuote(usage)+"; exit 0;; esac\nexec "+shellQuote(testshim.Bash(t))+" "+shellQuote(filepath.ToSlash(fake))+" \"$@\"\n")
	controlRunSelf(t, f, nil)
	control := NewControl(64, 2)
	startControlledRun(t, f, control, Args{Attempts: 1, MaxUnits: intPointer(1)})
	awaitEvent(t, control, "quota")
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)
	cp, err := ReadCheckpoint(f.root)
	if err != nil || cp.Stage != "quota" || cp.Unit != "P001.1.1" || cp.Agent != "claude" {
		t.Fatalf("checkpoint = %#v, err = %v", cp, err)
	}
	states := pausedStateEvents(t, f, "quota")
	if len(states) != 1 || states[0]["unit"] != "P001.1.1" {
		t.Fatalf("pause state events = %v", states)
	}
	if calls := f.calls(); len(calls) != 0 {
		t.Fatalf("an agent started although every executor waits: %d calls", len(calls))
	}
	assertPausedJournals(t, f, "paused")
}

// Pause after the recovered pending commit finished its transaction writes the
// complete checkpoint and publishes it; the recovered commit is not repeated.
func TestPauseAfterRecoveredPendingCommit(t *testing.T) {
	skipSlow(t)
	f := pendingCommitFixture(t)
	reached, release, before := gateOn(t, "commit")
	controlRunSelf(t, f, before)
	control := NewControl(64, 2)
	startControlledRun(t, f, control, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)})
	select {
	case <-reached:
	case <-time.After(15 * time.Second):
		t.Fatal("the runner did not reach the recovered commit")
	}
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	release()
	awaitPaused(t, control)
	cp, err := ReadCheckpoint(f.root)
	if err != nil || cp.Stage != "complete" || cp.Unit != "P001.1.1" {
		t.Fatalf("checkpoint = %#v, err = %v", cp, err)
	}
	states := pausedStateEvents(t, f, "complete")
	if len(states) != 1 || states[0]["unit"] != "P001.1.1" {
		t.Fatalf("pause state events = %v", states)
	}
	if calls := f.calls(); len(calls) != 0 {
		t.Fatalf("the recovered commit started an agent: %d calls", len(calls))
	}
	assertPausedJournals(t, f, "done")
}

// pendingCommitFixture leaves the unit with its commit made by hand and the
// done journal behind it: the next run finds the pending commit and recovers
// it instead of prompting the agent again.
func pendingCommitFixture(t *testing.T) *runFixture {
	t.Helper()
	f := newRunFixture(t)
	f.write("a_test.go", "package a\nfunc TestA() {}\nfunc TestB() {}\n")
	f.commit("tests")
	f.check(f.cli("prompt", "P001.1.1"), 0)
	f.write("a.txt", "a\n")
	f.write("a_test.go", "package a\nfunc TestA() {}\n")
	f.check(f.cli("verify", "P001.1.1"), 0)
	f.check(f.cli("done", "P001.1.1", "--handoff=done"), 0)
	// The interrupted commit: the exact message docflow commit expects, made
	// by hand so the journal done wrote stays behind.
	f.git("add", "-A")
	c := exec.Command("git", "commit", "-q", "-F", "-")
	c.Dir = f.root
	c.Stdin = strings.NewReader("P001.1.1: first unit\n\n- done\n\nTest changes:\n- a_test.go: -1 lines\n")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	return f
}

// assertNoPause checks that a run stopped by a signal or a canceled parent
// emitted no pause: no state event, no paused unit or run end, no checkpoint.
func assertNoPause(t *testing.T, f *runFixture) {
	t.Helper()
	for _, file := range eventFiles(t, f) {
		for _, record := range readEvents(t, file) {
			switch record["event"] {
			case "state":
				if record["state"] == "Paused" {
					t.Fatalf("a canceled run published a pause state: %v", record)
				}
			case "unit_end":
				if record["status"] == "paused" {
					t.Fatalf("a canceled run closed its unit as paused: %v", record)
				}
			case "run_end":
				if _, ok := record["paused"]; ok {
					t.Fatalf("a canceled run wrote a paused run_end: %v", record)
				}
			}
		}
	}
	if _, err := ReadCheckpoint(f.root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a canceled run wrote a checkpoint: %v", err)
	}
}

// A parent context canceled while the commit gate is up is not a Pause: the
// boundary completes the unit and publishes no pause state.
func TestCanceledParentAtCommitBoundaryIsNotPause(t *testing.T) {
	f := newRunFixture(t)
	f.steps(writeReply("a.txt"))
	reached, release, before := gateOn(t, "commit")
	controlRunSelf(t, f, before)
	control := NewControl(64, 2)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	startControlledRunWith(t, f, control, parent, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)})
	select {
	case <-reached:
	case <-time.After(15 * time.Second):
		t.Fatal("the runner did not reach the commit of the unit")
	}
	cancel()
	release()
	awaitDone(t, control)
	if got := control.State(); got == StatePaused {
		t.Fatalf("state = %s, want no pause", got)
	}
	if _, unit, err := cycle.FindUnit(f.root, "P001.1.1"); err != nil || unit.Status != "x" {
		t.Fatalf("unit status = %q, err = %v; the commit boundary did not complete", unit.Status, err)
	}
	assertNoPause(t, f)
}

// A parent context canceled during the executor-chain wait is not a Pause: no
// checkpoint, no state event, and the usual exit code.
func TestCanceledParentDuringExecutorChainWaitIsNotPause(t *testing.T) {
	f := executorRunFixture(t)
	reset := (time.Now().Unix()/60 + 120) * 60
	usage := "Current session: 100% used · resets " + time.Unix(reset, 0).UTC().Format("Jan 2 at 3:04pm") + " (UTC)"
	fake, err := filepath.Abs("../agentio/testdata/fake_agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	testshim.Script(t, filepath.Join(f.tmp, "bin"), "claude", "#!/bin/sh\ncase \"$*\" in *'/usage'*) printf '%s\\n' "+shellQuote(usage)+"; exit 0;; esac\nexec "+shellQuote(testshim.Bash(t))+" "+shellQuote(filepath.ToSlash(fake))+" \"$@\"\n")
	controlRunSelf(t, f, nil)
	control := NewControl(64, 2)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	startControlledRunWith(t, f, control, parent, Args{Attempts: 1, MaxUnits: intPointer(1)})
	awaitEvent(t, control, "quota")
	cancel()
	awaitDone(t, control)
	assertNoPause(t, f)
}

// A parent context canceled while the recovered pending commit runs is not a
// Pause either, although the same boundary under a real Pause publishes one.
func TestCanceledParentAfterRecoveredPendingCommitIsNotPause(t *testing.T) {
	skipSlow(t)
	f := pendingCommitFixture(t)
	reached, release, before := gateOn(t, "commit")
	controlRunSelf(t, f, before)
	control := NewControl(64, 2)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	startControlledRunWith(t, f, control, parent, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)})
	select {
	case <-reached:
	case <-time.After(15 * time.Second):
		t.Fatal("the runner did not reach the recovered commit")
	}
	cancel()
	release()
	awaitDone(t, control)
	assertNoPause(t, f)
}

// Without a control host a canceled run context stops the executor-chain wait
// with the usual code and never writes a pause.
func TestCanceledRunContextDuringExecutorChainWaitIsNotPause(t *testing.T) {
	f := executorRunFixture(t)
	reset := (time.Now().Unix()/60 + 120) * 60
	usage := "Current session: 100% used · resets " + time.Unix(reset, 0).UTC().Format("Jan 2 at 3:04pm") + " (UTC)"
	fake, err := filepath.Abs("../agentio/testdata/fake_agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	testshim.Script(t, filepath.Join(f.tmp, "bin"), "claude", "#!/bin/sh\ncase \"$*\" in *'/usage'*) printf '%s\\n' "+shellQuote(usage)+"; exit 0;; esac\nexec "+shellQuote(testshim.Bash(t))+" "+shellQuote(filepath.ToSlash(fake))+" \"$@\"\n")
	controlRunSelf(t, f, nil)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	var code int
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		code, runErr = runWithOptions(f.root, Args{Attempts: 1, MaxUnits: intPointer(1)}, io.Discard, io.Discard, "", runOptions{ctx: parent})
	}()
	awaitRecord(t, f, "quota")
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the canceled run did not stop")
	}
	if code != 7 || !errors.Is(runErr, context.Canceled) {
		t.Fatalf("canceled run = %d, %v, want 7, context.Canceled", code, runErr)
	}
	assertNoPause(t, f)
}

// awaitRecord waits until the run stream mentions an event type; an ordinary
// run has no controller to observe it live.
func awaitRecord(t *testing.T, f *runFixture, typ string) {
	t.Helper()
	needle := []byte(`"event":"` + typ + `"`)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		files, _ := filepath.Glob(filepath.Join(f.root, ".git/docflow/run/*.events.jsonl"))
		for _, file := range files {
			if data, err := os.ReadFile(file); err == nil && bytes.Contains(data, needle) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the run stream published no %q event", typ)
}

// A Pause during auto-replan restores the plan the agent was editing: the
// half-validated edit never survives, the checkpoint names the interrupted
// operation, and no second agent starts after Paused.
func TestPauseDuringReplanRestoresPlan(t *testing.T) {
	f := newReplanFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "replan-started")
	f.steps(blockReply(), "cp "+planFile+" .git/replan-before\n"+
		editPlan("- write b.txt", "- write b.txt and c.txt")+
		"touch "+shellQuote(marker)+"\nsleep 60 &\nwait\n")
	args := Args{Replans: 3, Attempts: 1, MaxUnits: intPointer(1)}
	control := NewControl(64, 2)
	startControlledRun(t, f, control, args)
	awaitMarker(t, f, marker, control)
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)
	if got, want := f.read(planFile), f.read(".git/replan-before"); got != want {
		t.Fatalf("the paused replan left its edit in the plan:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(f.read(planFile), "and c.txt") {
		t.Fatal("the replan edit survived the pause")
	}
	cp, err := ReadCheckpoint(f.root)
	if err != nil || cp.Stage != "replan" || cp.Unit != "P001.1.1" || cp.Agent != "claude" || len(cp.Logs) == 0 {
		t.Fatalf("checkpoint = %#v, err = %v", cp, err)
	}
	if states := pausedStateEvents(t, f, "replan"); len(states) != 1 || states[0]["unit"] != "P001.1.1" {
		t.Fatalf("pause state events = %v", states)
	}
	found := false
	for _, record := range fixtureEvents(t, f) {
		if record["event"] == "replan" {
			found = true
			if record["result"] != "paused" || record["exit_code"] != float64(0) {
				t.Fatalf("replan event = %v, want result paused and exit_code 0", record)
			}
		}
	}
	if !found {
		t.Fatal("the paused replan emitted no replan event")
	}
	if calls := f.calls(); len(calls) != 2 {
		t.Fatalf("agent calls = %d, want the unit and its replan only", len(calls))
	}
	assertPausedJournals(t, f, "blocked")
	if err := control.ResumeRun(context.Background(), f.root, args, ""); err == nil || !strings.Contains(err.Error(), "paused during replan") {
		t.Fatalf("resuming a replan checkpoint = %v, want an explicit non-resumable stop", err)
	}
}

// A Pause during the closing call writes the checkpoint of the interrupted
// operation, publishes the state, and makes no closing commit.
func TestPauseDuringCloseWritesCheckpoint(t *testing.T) {
	f := readyCloseFixture(t)
	marker := filepath.Join(f.tmp, "close-started")
	f.steps("touch " + shellQuote(marker) + "\nsleep 60 &\nwait\n")
	args := Args{Replans: 3, Attempts: 1, Close: true, RunArgs: agents.RunArgs{Config: f.config}}
	control := NewControl(64, 2)
	startControlledRun(t, f, control, args)
	awaitMarker(t, f, marker, control)
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)
	cp, err := ReadCheckpoint(f.root)
	if err != nil || cp.Stage != "close" || cp.Plan != "P001" || cp.Agent != "fake" || len(cp.Logs) == 0 {
		t.Fatalf("checkpoint = %#v, err = %v", cp, err)
	}
	if states := pausedStateEvents(t, f, "close"); len(states) != 1 {
		t.Fatalf("pause state events = %v", states)
	}
	if subject := f.git("log", "-1", "--format=%s"); subject == "P001: close plan" {
		t.Fatal("the paused closing still committed the plan")
	}
	if calls := f.calls(); len(calls) != 1 {
		t.Fatalf("agent calls = %d, want the closing call only", len(calls))
	}
	for _, record := range fixtureEvents(t, f) {
		if record["event"] == "close" {
			if record["result"] != "paused" || record["exit_code"] != float64(0) {
				t.Fatalf("close event = %v, want result paused and exit_code 0", record)
			}
		}
		if record["event"] == "run_end" {
			if record["paused"] != true || record["reason"] != "paused" || record["exit_code"] != float64(0) {
				t.Fatalf("paused run_end = %v", record)
			}
		}
	}
}

// A Pause during the provider refusal wait (a throttled pi call is retried on
// the same route after 30 s) stops the wait at once: the checkpoint names the
// provider wait, no second call or fallback starts, and the unit is paused.
func TestPauseDuringRefusalWait(t *testing.T) {
	f := fallbackExecutorRunFixture(t)
	controlRunSelf(t, f, nil)
	throttled := `429: {"code":"1302","message":"Rate limit reached for requests"}`
	f.steps(emit(map[string]any{"type": "session", "id": "pi-throttled"}) +
		emit(map[string]any{"type": "message_end", "message": map[string]any{
			"role": "assistant", "content": []any{}, "stopReason": "error", "errorMessage": throttled,
		}}) +
		emit(map[string]any{"type": "agent_settled", "aborted": false}))
	control := NewControl(64, 2)
	startControlledRun(t, f, control, Args{Attempts: 3, MaxUnits: intPointer(1)})
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(fixtureJournalText(t, f), `"event":"stream"`) {
		if time.Now().After(deadline) {
			t.Fatal("the throttled call did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The refusal is classified right after the stream link; leave the
	// runner time to enter its 30 s wait.
	time.Sleep(500 * time.Millisecond)
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)
	cp, err := ReadCheckpoint(f.root)
	if err != nil || cp.Stage != "quota" || cp.Unit != "P001.1.1" || cp.Agent != "pi" {
		t.Fatalf("checkpoint = %#v, err = %v", cp, err)
	}
	if calls := f.calls(); len(calls) != 1 {
		t.Fatalf("agent calls = %d, want only the throttled call", len(calls))
	}
	for _, record := range fixtureEvents(t, f) {
		if record["event"] == "route_switch" {
			t.Fatalf("a Pause in the refusal wait switched routes: %v", record)
		}
	}
	assertPausedJournals(t, f, "paused")
}

// fixtureJournalText is the raw text of every run stream of the fixture.
func fixtureJournalText(t *testing.T, f *runFixture) string {
	t.Helper()
	var out strings.Builder
	files, _ := filepath.Glob(filepath.Join(f.root, ".git/docflow/run/*.events.jsonl"))
	for _, file := range files {
		data, _ := os.ReadFile(file)
		out.Write(data)
	}
	return out.String()
}

// stopNowRecords checks the deferred operation event and run_end of a
// controlled Stop now: both read as the action, not as a runner failure.
func stopNowRecords(t *testing.T, f *runFixture, operation string) {
	t.Helper()
	found := false
	for _, record := range fixtureEvents(t, f) {
		switch record["event"] {
		case operation:
			found = true
			if record["result"] != "stopped" || record["exit_code"] != float64(0) {
				t.Fatalf("%s event = %v, want result stopped and exit_code 0", operation, record)
			}
		case "run_end":
			if record["reason"] != "stopped" || record["exit_code"] != float64(0) {
				t.Fatalf("stopped run_end = %v", record)
			}
		}
	}
	if !found {
		t.Fatalf("the stopped run emitted no %s event", operation)
	}
}

// A Stop now during auto-replan restores the plan the agent was editing and
// journals the replan as stopped, like run_end; no checkpoint is written.
func TestStopNowDuringReplanRestoresPlan(t *testing.T) {
	f := newReplanFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "replan-started")
	f.steps(blockReply(), "cp "+planFile+" .git/replan-before\n"+
		editPlan("- write b.txt", "- write b.txt and c.txt")+
		"touch "+shellQuote(marker)+"\nsleep 60 &\nwait\n")
	control := NewControl(64, 2)
	startControlledRun(t, f, control, Args{Replans: 3, Attempts: 1, MaxUnits: intPointer(1)})
	awaitMarker(t, f, marker, control)
	if err := control.StopNow(); err != nil {
		t.Fatal(err)
	}
	awaitFinished(t, control)
	if got, want := f.read(planFile), f.read(".git/replan-before"); got != want {
		t.Fatalf("the stopped replan left its edit in the plan:\n%s\nwant:\n%s", got, want)
	}
	stopNowRecords(t, f, "replan")
	if _, err := ReadCheckpoint(f.root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a Stop now wrote a checkpoint: %v", err)
	}
}

// Without a host the cancellation keeps its old journal shape, but the
// unfinished replan edit is still not left in the plan.
func TestCanceledReplanRestoresPlan(t *testing.T) {
	f := newReplanFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "replan-started")
	f.steps(blockReply(), "cp "+planFile+" .git/replan-before\n"+
		editPlan("- write b.txt", "- write b.txt and c.txt")+
		"touch "+shellQuote(marker)+"\nsleep 60 &\nwait\n")
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	var code int
	go func() {
		defer close(done)
		code, _ = runWithOptions(f.root, Args{Replans: 3, Attempts: 1, MaxUnits: intPointer(1)}, io.Discard, io.Discard, "", runOptions{ctx: parent})
	}()
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the replan agent did not start")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the canceled run did not stop")
	}
	if got, want := f.read(planFile), f.read(".git/replan-before"); got != want {
		t.Fatalf("the canceled replan left its edit in the plan:\n%s\nwant:\n%s", got, want)
	}
	for _, record := range fixtureEvents(t, f) {
		if record["event"] == "replan" && record["result"] != "error" {
			t.Fatalf("canceled replan event = %v, want result error", record)
		}
	}
	if code == 0 {
		t.Fatal("a canceled run reported success")
	}
}

// A Stop now during the closing call journals the close as stopped and makes
// no closing commit.
func TestStopNowDuringCloseJournalsStopped(t *testing.T) {
	f := readyCloseFixture(t)
	marker := filepath.Join(f.tmp, "close-started")
	f.steps("touch " + shellQuote(marker) + "\nsleep 60 &\nwait\n")
	control := NewControl(64, 2)
	startControlledRun(t, f, control, Args{Replans: 3, Attempts: 1, Close: true, RunArgs: agents.RunArgs{Config: f.config}})
	awaitMarker(t, f, marker, control)
	if err := control.StopNow(); err != nil {
		t.Fatal(err)
	}
	awaitFinished(t, control)
	if subject := f.git("log", "-1", "--format=%s"); subject == "P001: close plan" {
		t.Fatal("the stopped closing still committed the plan")
	}
	stopNowRecords(t, f, "close")
}

// The closing agent archives and deletes the plan itself; the checkpoint
// still carries the fingerprint taken before the call.
func TestPauseDuringCloseAfterArchiveKeepsFingerprint(t *testing.T) {
	f := readyCloseFixture(t)
	marker := filepath.Join(f.tmp, "close-archived")
	f.steps(closeArchiveStep(f.binary) + "touch " + shellQuote(marker) + "\nsleep 60 &\nwait\n")
	args := Args{Replans: 3, Attempts: 1, Close: true, RunArgs: agents.RunArgs{Config: f.config}}
	control := NewControl(64, 2)
	startControlledRun(t, f, control, args)
	awaitMarker(t, f, marker, control)
	if _, err := os.Stat(filepath.Join(f.root, planFile)); !os.IsNotExist(err) {
		t.Fatalf("the closing agent did not remove the plan: %v", err)
	}
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)
	cp, err := ReadCheckpoint(f.root)
	if err != nil || cp.Stage != "close" || cp.Plan != "P001" || cp.PlanHash == "" {
		t.Fatalf("checkpoint = %#v, err = %v", cp, err)
	}
	if states := pausedStateEvents(t, f, "close"); len(states) != 1 {
		t.Fatalf("pause state events = %v", states)
	}
	for _, record := range fixtureEvents(t, f) {
		switch record["event"] {
		case "close":
			if record["result"] != "paused" || record["exit_code"] != float64(0) {
				t.Fatalf("close event = %v, want result paused and exit_code 0", record)
			}
		case "run_end":
			if record["paused"] != true || record["reason"] != "paused" || record["exit_code"] != float64(0) {
				t.Fatalf("paused run_end = %v", record)
			}
		}
	}
}

// A failed checkpoint write is a runner error, not a pause: the host fails,
// the journal shows error/2 and no state event is published.
func TestPauseCheckpointWriteFailureIsRunnerError(t *testing.T) {
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "agent-started")
	f.steps("touch " + shellQuote(marker) + "\nsleep 60 &\nwait\n")
	blocker := filepath.Join(f.root, ".git/docflow/control")
	if err := os.MkdirAll(filepath.Dir(blocker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	control := NewControl(32, 2)
	startControlledRun(t, f, control, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)})
	awaitMarker(t, f, marker, control)
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitDone(t, control)
	if got := control.State(); got != StateFailed {
		t.Fatalf("state = %s, want Failed", got)
	}
	if _, err := ReadCheckpoint(f.root); err == nil {
		t.Fatal("a failed write left a checkpoint")
	}
	for _, record := range fixtureEvents(t, f) {
		switch record["event"] {
		case "state":
			t.Fatalf("a failed checkpoint write published a pause state: %v", record)
		case "unit_end":
			if record["status"] != "error" {
				t.Fatalf("unit_end = %v, want status error", record)
			}
		case "run_end":
			if _, ok := record["paused"]; ok {
				t.Fatalf("run_end = %v, want no paused field", record)
			}
			if record["exit_code"] != float64(2) || record["reason"] == "paused" {
				t.Fatalf("run_end = %v, want exit_code 2 and the write error", record)
			}
		}
	}
}

// A Pause that ends the pending-commit recovery's verify is a pause, not a
// failed check: the checkpoint names the complete boundary and the next run
// still finds the pending commit.
func TestPauseDuringPendingCommitRecoveryFailureIsPause(t *testing.T) {
	f := pendingCommitFixture(t)
	previous := runnerSelf
	reached, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	runnerSelf = func(root string, args ...string) cycle.Result {
		if len(args) > 0 && args[0] == "verify" {
			once.Do(func() { close(reached) })
			<-release
			return cycle.Result{Code: 2, Stderr: "verify broke"}
		}
		return previous(root, args...)
	}
	t.Cleanup(func() { runnerSelf = previous })
	control := NewControl(64, 2)
	startControlledRun(t, f, control, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)})
	select {
	case <-reached:
	case <-time.After(15 * time.Second):
		t.Fatal("the runner did not reach the recovered verify")
	}
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	close(release)
	awaitPaused(t, control)
	cp, err := ReadCheckpoint(f.root)
	if err != nil || cp.Stage != "complete" || cp.Unit != "P001.1.1" {
		t.Fatalf("checkpoint = %#v, err = %v", cp, err)
	}
	if states := pausedStateEvents(t, f, "complete"); len(states) != 1 || states[0]["unit"] != "P001.1.1" {
		t.Fatalf("pause state events = %v", states)
	}
	if calls := f.calls(); len(calls) != 0 {
		t.Fatalf("the failed recovery started an agent: %d calls", len(calls))
	}
	assertPausedJournals(t, f, "paused")
}

// seamPause installs a Pause on one boundary seam: the test waits for the
// marker and asks the host to pause, and the seam returns only once the run
// context is canceled, so the check after the seam sees a Pause.
func seamPause(t *testing.T, seam *func(context.Context, string), boundary, marker string) {
	t.Helper()
	previous := *seam
	*seam = func(ctx context.Context, at string) {
		if at != boundary {
			return
		}
		if err := os.WriteFile(marker, nil, 0o644); err != nil {
			return
		}
		<-ctx.Done()
	}
	t.Cleanup(func() { *seam = previous })
}

// A Pause that arrives after a successful replan call, or during the checks
// before its commit, stops the operation: the plan is restored, nothing is
// committed, and no second agent starts.
func TestPauseAfterSuccessfulReplanCallStopsBeforeCommit(t *testing.T) {
	skipSlow(t)
	for _, boundary := range []string{"after-agent", "before-commit"} {
		t.Run(boundary, func(t *testing.T) {
			f := newReplanFixture(t)
			controlRunSelf(t, f, nil)
			head := f.git("rev-parse", "HEAD")
			marker := filepath.Join(f.tmp, "replan-seam")
			seamPause(t, &replanSeam, boundary, marker)
			f.steps(blockReply(), "cp "+planFile+" .git/replan-before\n"+liftReply("P001.1.1"))
			args := Args{Replans: 3, Attempts: 1, MaxUnits: intPointer(1)}
			control := NewControl(64, 2)
			startControlledRun(t, f, control, args)
			awaitMarker(t, f, marker, control)
			if err := control.Pause(); err != nil {
				t.Fatal(err)
			}
			awaitPaused(t, control)
			if got, want := f.read(planFile), f.read(".git/replan-before"); got != want {
				t.Fatalf("the paused replan kept its edit:\n%s\nwant:\n%s", got, want)
			}
			if strings.Contains(f.read(planFile), "### [~] P001.1.1") {
				t.Fatal("the replan edit survived the pause")
			}
			if got := f.git("rev-parse", "HEAD"); got != head {
				t.Fatalf("HEAD moved to %s, want %s", got, head)
			}
			cp, err := ReadCheckpoint(f.root)
			if err != nil || cp.Stage != "replan" || cp.Unit != "P001.1.1" || cp.PlanHash == "" || len(cp.Logs) == 0 {
				t.Fatalf("checkpoint = %#v, err = %v", cp, err)
			}
			if states := pausedStateEvents(t, f, "replan"); len(states) != 1 || states[0]["unit"] != "P001.1.1" {
				t.Fatalf("pause state events = %v", states)
			}
			found := false
			for _, record := range fixtureEvents(t, f) {
				if record["event"] == "replan" {
					found = true
					if record["result"] != "paused" || record["exit_code"] != float64(0) {
						t.Fatalf("replan event = %v, want result paused and exit_code 0", record)
					}
				}
			}
			if !found {
				t.Fatal("the paused replan emitted no replan event")
			}
			if calls := f.calls(); len(calls) != 2 {
				t.Fatalf("agent calls = %d, want the unit and its replan only", len(calls))
			}
			assertPausedJournals(t, f, "blocked")
			if err := control.ResumeRun(context.Background(), f.root, args, ""); err == nil || !strings.Contains(err.Error(), "paused during replan") {
				t.Fatalf("resuming a replan checkpoint = %v, want an explicit non-resumable stop", err)
			}
		})
	}
}

// A Pause after a successful closing call and before the closing commit keeps
// the fingerprint taken before the call and commits nothing.
func TestPauseAfterSuccessfulCloseCallStopsBeforeCommit(t *testing.T) {
	f := readyCloseFixture(t)
	marker := filepath.Join(f.tmp, "close-seam")
	seamPause(t, &closeSeam, "after-agent", marker)
	f.steps(closeArchiveStep(f.binary) + doneReply())
	args := Args{Replans: 3, Attempts: 1, Close: true, RunArgs: agents.RunArgs{Config: f.config}}
	control := NewControl(64, 2)
	startControlledRun(t, f, control, args)
	awaitMarker(t, f, marker, control)
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)
	cp, err := ReadCheckpoint(f.root)
	if err != nil || cp.Stage != "close" || cp.Plan != "P001" || cp.PlanHash == "" || len(cp.Logs) == 0 {
		t.Fatalf("checkpoint = %#v, err = %v", cp, err)
	}
	if subject := f.git("log", "-1", "--format=%s"); subject == "P001: close plan" {
		t.Fatal("the paused closing still committed the plan")
	}
	if states := pausedStateEvents(t, f, "close"); len(states) != 1 {
		t.Fatalf("pause state events = %v", states)
	}
	for _, record := range fixtureEvents(t, f) {
		switch record["event"] {
		case "close":
			if record["result"] != "paused" || record["exit_code"] != float64(0) {
				t.Fatalf("close event = %v, want result paused and exit_code 0", record)
			}
		case "run_end":
			if record["paused"] != true || record["reason"] != "paused" || record["exit_code"] != float64(0) {
				t.Fatalf("paused run_end = %v", record)
			}
		}
	}
}

// A Pause that arrives after the closing commit has begun is not a pause of
// the run: the operation is done, the journal says done, the host is Finished
// and no checkpoint appears.
func TestPauseAfterCloseCommitIsFinished(t *testing.T) {
	f := readyCloseFixture(t)
	controlRunSelf(t, f, nil)
	control := NewControl(64, 2)
	previous := closeSeam
	closeSeam = func(ctx context.Context, at string) {
		if at != "after-commit" {
			return
		}
		_ = control.Pause()
	}
	t.Cleanup(func() { closeSeam = previous })
	f.steps(closeArchiveStep(f.binary) + doneReply())
	args := Args{Replans: 3, Attempts: 1, Close: true, RunArgs: agents.RunArgs{Config: f.config}}
	startControlledRun(t, f, control, args)
	awaitDone(t, control)
	if got := control.State(); got != StateFinished {
		t.Fatalf("state = %s, want Finished", got)
	}
	if _, err := ReadCheckpoint(f.root); err == nil {
		t.Fatal("a pause after the commit wrote a checkpoint")
	}
	if subject := f.git("log", "-1", "--format=%s"); subject != "P001: close plan" {
		t.Fatalf("closing commit = %q", subject)
	}
	for _, record := range fixtureEvents(t, f) {
		switch record["event"] {
		case "close":
			if record["result"] != "done" || record["exit_code"] != float64(0) {
				t.Fatalf("close event = %v, want result done and exit_code 0", record)
			}
		case "run_end":
			if _, ok := record["paused"]; ok {
				t.Fatalf("run_end = %v, want no paused field", record)
			}
			if record["exit_code"] != float64(0) || record["reason"] == "paused" {
				t.Fatalf("run_end = %v, want the closing notification and exit_code 0", record)
			}
		}
	}
}

// A Pause that arrives after the replan commit has begun is not a pause of the
// run either: the replan is done, the journal says done, the host is Finished
// and no checkpoint appears.
func TestPauseAfterReplanCommitIsFinished(t *testing.T) {
	f := newReplanFixture(t)
	controlRunSelf(t, f, nil)
	control := NewControl(64, 2)
	previous := replanSeam
	replanSeam = func(ctx context.Context, at string) {
		if at != "after-commit" {
			return
		}
		_ = control.Pause()
	}
	t.Cleanup(func() { replanSeam = previous })
	f.steps(blockReply(), liftReply("P001.1.1"))
	args := Args{Replans: 3, Attempts: 1, MaxUnits: intPointer(1)}
	startControlledRun(t, f, control, args)
	awaitDone(t, control)
	if got := control.State(); got != StateFinished {
		t.Fatalf("state = %s, want Finished", got)
	}
	if _, err := ReadCheckpoint(f.root); err == nil {
		t.Fatal("a pause after the commit wrote a checkpoint")
	}
	if subject := f.git("log", "-1", "--format=%s"); !strings.Contains(subject, "replan P001.1.1") {
		t.Fatalf("replan commit = %q", subject)
	}
	for _, record := range fixtureEvents(t, f) {
		switch record["event"] {
		case "replan":
			if record["result"] != "done" || record["exit_code"] != float64(0) {
				t.Fatalf("replan event = %v, want result done and exit_code 0", record)
			}
		case "run_end":
			if _, ok := record["paused"]; ok {
				t.Fatalf("run_end = %v, want no paused field", record)
			}
			if record["exit_code"] != float64(0) || record["reason"] == "paused" {
				t.Fatalf("run_end = %v, want exit_code 0", record)
			}
		}
	}
}

// LoadRecovery is what the terminal workspace shows after a reopen: the saved
// stage and unit, the last journal outcome, and a refusal once the checkout no
// longer matches the checkpoint.
func TestLoadRecoveryDescribesPausedRun(t *testing.T) {
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "agent-started")
	f.steps(emit(map[string]any{"type": "system", "subtype": "init", "session_id": "recovery-session"}) +
		"touch " + shellQuote(marker) + "\n" +
		"sleep 60 &\nwait\n")
	control := NewControl(64, 2)
	startControlledRun(t, f, control, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)})
	awaitMarker(t, f, marker, control)
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)

	recovery, err := LoadRecovery(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if recovery.Checkpoint == nil || !recovery.Resumable() {
		t.Fatalf("recovery = %#v, want a resumable checkpoint", recovery)
	}
	if recovery.Checkpoint.Unit != "P001.1.1" || recovery.Checkpoint.Stage != "agent" {
		t.Fatalf("checkpoint = %#v, want P001.1.1 during an agent call", recovery.Checkpoint)
	}
	if recovery.LastRun == nil || recovery.LastRun.Reason != "paused" || !recovery.LastRun.Paused || recovery.LastRun.ExitCode != 0 {
		t.Fatalf("last run = %#v, want the paused outcome", recovery.LastRun)
	}
	if recovery.LastRun.Unit != "P001.1.1" || recovery.LastRun.UnitStatus != "paused" {
		t.Fatalf("last run unit = %#v, want the paused unit", recovery.LastRun)
	}

	// An edited plan is a conflict even though the checkpoint is still
	// readable: Continue must not silently resume the wrong work.
	f.write(planFile, strings.ReplaceAll(f.read(planFile), "title: Plan", "title: Renamed"))
	recovery, err = LoadRecovery(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if recovery.Resumable() || !strings.Contains(recovery.Problem, "changed since the pause") {
		t.Fatalf("recovery after the plan edit = %#v, want a plan conflict", recovery)
	}
}

// A checkpoint written at the complete boundary belongs to a finished unit:
// reopening must not offer to continue it as interrupted work.
func TestLoadRecoveryAfterCompletedUnitHasNothingToContinue(t *testing.T) {
	skipSlow(t)
	f := newRunFixture(t)
	f.steps(writeReply("a.txt"))
	reached, release, before := gateOn(t, "commit")
	controlRunSelf(t, f, before)
	control := NewControl(64, 2)
	startControlledRun(t, f, control, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)})
	select {
	case <-reached:
	case <-time.After(15 * time.Second):
		t.Fatal("the runner did not reach the commit of the unit")
	}
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	release()
	awaitPaused(t, control)

	recovery, err := LoadRecovery(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if recovery.Checkpoint == nil || recovery.Checkpoint.Stage != "complete" {
		t.Fatalf("checkpoint = %#v, want the complete boundary", recovery.Checkpoint)
	}
	if !recovery.Completed || recovery.Resumable() || recovery.PendingCommit {
		t.Fatalf("recovery = %#v, want a completed unit with no pending commit", recovery)
	}
	if recovery.LastRun == nil || recovery.LastRun.UnitStatus != "done" {
		t.Fatalf("last run = %#v, want the done unit", recovery.LastRun)
	}
}

// A done unit whose commit was interrupted is not "work to continue", but its
// pending commit is still recovered by the existing cycle: LoadRecovery names
// it, and ResumeRun finishes the commit without calling the agent again.
func TestLoadRecoveryRestoresPendingCommit(t *testing.T) {
	skipSlow(t)
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	f.check(f.cli("prompt", "P001.1.1"), 0)
	f.write("a.txt", "a\n")
	f.check(f.cli("verify", "P001.1.1"), 0)
	f.check(f.cli("done", "P001.1.1", "--handoff=done"), 0)
	head := f.git("rev-parse", "HEAD")
	data, err := os.ReadFile(filepath.Join(f.root, planFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteCheckpoint(f.root, Checkpoint{
		Plan: "P001", Unit: "P001.1.1", Head: head, PlanHash: PlanFingerprint(data), Stage: "commit",
	}); err != nil {
		t.Fatal(err)
	}
	recovery, err := LoadRecovery(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if recovery.Checkpoint == nil || !recovery.PendingCommit || recovery.Completed || !recovery.Resumable() {
		t.Fatalf("recovery = %#v, want a resumable pending commit", recovery)
	}

	control := NewControl(64, 2)
	if err := control.ResumeRun(context.Background(), f.root, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)}, ""); err != nil {
		t.Fatal(err)
	}
	awaitFinished(t, control)
	if subject := f.git("log", "-1", "--format=%s"); !strings.Contains(subject, "P001.1.1") {
		t.Fatalf("HEAD commit = %q, want the recovered unit commit", subject)
	}
	if calls := f.calls(); len(calls) != 0 {
		t.Fatalf("agent calls after recovering the pending commit = %d, want none", len(calls))
	}
}

// A checkpoint pinned to another assignment starts one new session with the
// full prompt instead of pairing the saved session with a different route.
func TestResumeWithChangedAssignmentStartsOneNewSession(t *testing.T) {
	skipSlow(t)
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "agent-started")
	f.steps(emit(map[string]any{"type": "system", "subtype": "init", "session_id": "old-route-session"}) +
		"touch " + shellQuote(marker) + "\n" +
		"sleep 60 &\nwait\n")
	args := Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)}
	control := NewControl(64, 2)
	startControlledRun(t, f, control, args)
	awaitMarker(t, f, marker, control)
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)
	checkpoint, err := ReadCheckpoint(f.root)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.Effort = "max"
	if err := WriteCheckpoint(f.root, checkpoint); err != nil {
		t.Fatal(err)
	}
	f.steps(writeReply("a.txt"))
	if err := control.ResumeRun(context.Background(), f.root, args, ""); err != nil {
		t.Fatal(err)
	}
	awaitFinished(t, control)
	calls := f.calls()
	if len(calls) != 1 {
		t.Fatalf("agent calls = %d, want one new session", len(calls))
	}
	if argv := strings.Join(calls[0].Argv, " "); strings.Contains(argv, "--resume") {
		t.Fatalf("a changed assignment reused the old session: %s", argv)
	}
	for _, want := range []string{"## Recovery", "the paused assignment is not available on this route"} {
		if !strings.Contains(calls[0].Stdin, want) {
			t.Fatalf("new session prompt lacks %q:\n%s", want, calls[0].Stdin)
		}
	}
	if _, unit, err := cycle.FindUnit(f.root, "P001.1.1"); err != nil || unit.Status != "x" {
		t.Fatalf("unit status = %q, err = %v", unit.Status, err)
	}
}

// The checkout lock refuses a second owner, a new instance never removes a
// lock that names another live process, and a stale file does not block
// recovery forever.
func TestRunLockRefusesLiveOwnerAndRecoversStaleFile(t *testing.T) {
	f := newRunFixture(t)
	release, err := runLock(f.root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.root, ".git", "docflow", "run", "run.lock")
	if _, err := runLock(f.root); err == nil {
		t.Fatal("a second owner took the checkout lock")
	}

	sleeper := exec.Command("sleep", "30")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sleeper.Process.Kill()
		_, _ = sleeper.Process.Wait()
	})
	if err := os.WriteFile(path, []byte(strconv.Itoa(sleeper.Process.Pid)+"\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("release removed a lock of another live process: %v", err)
	}
	if _, err := runLock(f.root); err == nil {
		t.Fatal("a live foreign lock was overwritten")
	}

	if err := os.WriteFile(path, []byte("99999999\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	staleRelease, err := runLock(f.root)
	if err != nil {
		t.Fatalf("a stale lock blocked a new owner: %v", err)
	}
	staleRelease()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the stale lock was left behind: %v", err)
	}
}
