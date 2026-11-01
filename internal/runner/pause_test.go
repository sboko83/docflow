package runner

import (
	"bytes"
	"context"
	"errors"
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
	if err := control.StartRun(context.Background(), f.root, args, ""); err != nil {
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
			return writePauseCheckpoint(ctx, f.root, "P001", "P001.1.1", "quota", agents.Route{Agent: "fake"}, "", 0, 0, nil)
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
}

func intPointer(value int) *int { return &value }
