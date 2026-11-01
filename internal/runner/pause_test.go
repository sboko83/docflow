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
	f.steps("touch " + shellQuote(marker) + "\n" + emit(map[string]any{"type": "system", "subtype": "init", "session_id": "pause-session"}) + "sleep 60 &\necho $! > " + shellQuote(pidFile) + "\nwait\n")
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
	if pidAlive(pid) {
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
			_, err := writePauseCheckpoint(ctx, f.root, "P001", "P001.1.1", "quota", agents.Route{Agent: "fake"}, "", 0, 0, 0, nil)
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
	f.steps("touch " + shellQuote(marker) + "\n" +
		emit(map[string]any{"type": "system", "subtype": "init", "session_id": "pause-claude", "model": "claude-opus-5-5"}) +
		emit(map[string]any{"type": "assistant", "message": map[string]any{
			"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "working"}}}}) +
		emit(map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{"status": "rejected", "resetsAt": reset}}) +
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
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "agent-started")
	f.steps("touch " + shellQuote(marker) + "\n" +
		emit(map[string]any{"type": "system", "subtype": "init", "session_id": "paused-session"}) +
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
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	first := filepath.Join(f.tmp, "first-started")
	second := filepath.Join(f.tmp, "second-started")
	kickoff := emit(map[string]any{"type": "system", "subtype": "init", "session_id": "pause-session"})
	f.steps("touch " + shellQuote(first) + "\n" + kickoff + "sleep 60 &\nwait\n")
	args := Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 2, MaxUnits: intPointer(1)}
	control := NewControl(64, 2)
	startControlledRun(t, f, control, args)
	awaitMarker(t, f, first, control)
	if err := control.Pause(); err != nil {
		t.Fatal(err)
	}
	awaitPaused(t, control)
	f.steps("touch " + shellQuote(second) + "\n" +
		emit(map[string]any{"type": "system", "subtype": "init", "session_id": "pause-session-2"}) +
		"sleep 60 &\nwait\n")
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
}

// A checkpoint survives the process: a fresh controller (Idle, as after
// reopening the application) resumes the paused unit.
func TestResumeFromAFreshController(t *testing.T) {
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "agent-started")
	f.steps("touch " + shellQuote(marker) + "\n" +
		emit(map[string]any{"type": "system", "subtype": "init", "session_id": "reopen-session"}) +
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

// A Pause ending a rejected resume is classified before the rejected-resume
// retry: the canceled call is the only start of the resumed run, and the
// runner never opens a replacement session with an already canceled context.
func TestPauseBeforeRejectedResumeHandlingStartsNoNewSession(t *testing.T) {
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	marker := filepath.Join(f.tmp, "agent-started")
	f.steps("touch " + shellQuote(marker) + "\n" +
		emit(map[string]any{"type": "system", "subtype": "init", "session_id": "paused-session"}) +
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
