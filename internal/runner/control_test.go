package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/agents"
)

func TestControlCommandsAreExclusive(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	c := NewControl(2, 1)
	started, finish := make(chan struct{}), make(chan struct{})
	if err := c.Start(context.Background(), func(ctx context.Context, host *Control) error {
		close(started)
		<-finish
		// A Pause is only reported once its checkpoint is written.
		return host.checkpoint(root, Checkpoint{Plan: "P001", Head: "abc", Stage: "agent"})
	}); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := c.Start(context.Background(), func(context.Context, *Control) error { return nil }); err == nil {
		t.Fatal("second Start accepted")
	}
	if !c.MarkRunning() {
		t.Fatalf("state = %s", c.State())
	}
	if err := c.Pause(); err != nil {
		t.Fatal(err)
	}
	if err := c.StopAfterUnit(); err == nil {
		t.Fatal("concurrent stop accepted")
	}
	close(finish)
	<-c.Done()
	if got := c.State(); got != StatePaused {
		t.Fatalf("state = %s, want %s", got, StatePaused)
	}
}

func TestControlCancelsStartRequest(t *testing.T) {
	c := NewControl(1, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := c.Ask(ctx, Request{Kind: "confirm_start"}); result <- err }()
	select {
	case <-c.Requests():
	case <-time.After(time.Second):
		t.Fatal("confirmation request not published")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Ask error = %v", err)
	}
}

func TestControlCancelStartStopsBeforeExecution(t *testing.T) {
	c := NewControl(1, 1)
	started := make(chan struct{})
	if err := c.Start(context.Background(), func(ctx context.Context, host *Control) error {
		close(started)
		_, err := host.Ask(ctx, Request{Kind: "confirm_start"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	<-started
	<-c.Requests()
	if err := c.CancelStart(); err != nil {
		t.Fatal(err)
	}
	<-c.Done()
	if got := c.State(); got != StateCancelled {
		t.Fatalf("state = %s, want %s", got, StateCancelled)
	}
	if c.Checkpointed() {
		t.Fatal("a cancelled start wrote a pause checkpoint")
	}
	if err := c.Start(context.Background(), func(context.Context, *Control) error { return nil }); err != nil {
		t.Fatalf("start after a cancelled start: %v", err)
	}
	<-c.Done()
	if got := c.State(); got != StateFinished {
		t.Fatalf("state after the new run = %s, want %s", got, StateFinished)
	}
}

func TestControlCanStartAgainAfterCompletion(t *testing.T) {
	c := NewControl(1, 1)
	for i := 0; i < 2; i++ {
		if err := c.Start(context.Background(), func(context.Context, *Control) error { return nil }); err != nil {
			t.Fatal(err)
		}
		<-c.Done()
		if got := c.State(); got != StateFinished {
			t.Fatalf("iteration %d state = %s", i, got)
		}
	}
}

func TestControlCheckoutLockIsExclusive(t *testing.T) {
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-q", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := runDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.lock"), []byte(strconv.Itoa(os.Getppid())), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewControl(1, 1)
	if err := c.StartCheckout(context.Background(), root, func(context.Context, *Control) error { return nil }); err == nil {
		t.Fatal("StartCheckout accepted an occupied lock")
	}
	if got := c.State(); got != StateIdle {
		t.Fatalf("state after lock failure = %s", got)
	}
}

func TestControlDropsEventsForSlowObserver(t *testing.T) {
	c := NewControl(1, 1)
	c.Publish(Event{Type: "one"})
	c.Publish(Event{Type: "two"})
	select {
	case got := <-c.Events():
		if got.Type != "two" {
			t.Fatalf("event = %q, want latest", got.Type)
		}
	default:
		t.Fatal("event queue unexpectedly empty")
	}
}

// The checkout lock belongs to StartCheckout for the whole execution: a second
// owner is refused while it runs, and the file survives until execute returns.
func TestControlCheckoutLockOwnedForExecution(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	dir, err := runDir(root)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(dir, "run.lock")
	c := NewControl(2, 1)
	started, proceed := make(chan struct{}), make(chan struct{})
	inside := make(chan error, 1)
	if err := c.StartCheckout(context.Background(), root, func(context.Context, *Control) error {
		close(started)
		<-proceed
		data, err := os.ReadFile(lockPath)
		if err != nil {
			inside <- err
			return nil
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid != os.Getpid() {
			inside <- fmt.Errorf("lock pid = %q, err=%v", data, err)
			return nil
		}
		if _, err := runLock(root); err == nil {
			inside <- errors.New("a second owner acquired the checkout lock")
		} else {
			inside <- nil
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := NewControl(1, 1).StartCheckout(context.Background(), root, func(context.Context, *Control) error { return nil }); err == nil {
		t.Fatal("a second StartCheckout shared the checkout lock")
	}
	close(proceed)
	<-c.Done()
	if err := <-inside; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock left after execute returned: %v", err)
	}
	release, err := runLock(root)
	if err != nil {
		t.Fatalf("a new owner was refused after release: %v", err)
	}
	release()
}

// Release removes only the file this acquisition wrote: a lock another live
// process now owns stays in place for run --stop and the next runner.
func TestRunLockReleasesOnlyItsOwnFile(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	dir, err := runDir(root)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(dir, "run.lock")
	release, err := runLock(root)
	if err != nil {
		t.Fatal(err)
	}
	foreign := os.Getppid()
	if foreign == os.Getpid() || !pidAlive(foreign) {
		t.Skip("no live foreign pid available")
	}
	if err := os.WriteFile(lockPath, []byte(strconv.Itoa(foreign)), 0o600); err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("release removed a lock owned by pid %d: %v", foreign, err)
	}
}

// A controlled run delegating to runWithOptions must not drop the lock the
// outer owner holds, and the owner releases it when execute returns.
func TestControlledRunKeepsTheOuterCheckoutLock(t *testing.T) {
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	f.steps(writeReply("a.txt"))
	dir, err := runDir(f.root)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(dir, "run.lock")
	c := NewControl(16, 2)
	held := make(chan bool, 1)
	err = c.StartCheckout(context.Background(), f.root, func(ctx context.Context, host *Control) error {
		code, runErr := runWithOptions(f.root, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1), Yes: true}, io.Discard, io.Discard, "", runOptions{ctx: ctx, control: host, locked: true})
		if runErr != nil {
			return runErr
		}
		if code != 0 {
			return fmt.Errorf("run exited with code %d", code)
		}
		_, statErr := os.Stat(lockPath)
		held <- statErr == nil
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	<-c.Done()
	if got := c.State(); got != StateFinished {
		t.Fatalf("state = %s, want %s", got, StateFinished)
	}
	if !<-held {
		t.Fatal("the inner run removed the outer checkout lock")
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock left after the owner returned: %v", err)
	}
}

// TestControlDeclinedStartIsCancelled keeps a declined confirmation distinct
// from a finished run: no work began, so nothing may read as a completed run
// or as a resumable pause.
func TestControlDeclinedStartIsCancelled(t *testing.T) {
	f := newRunFixture(t)
	c := NewControl(16, 2)
	if err := c.StartRun(context.Background(), f.root, Args{Attempts: 1, Replans: 1, NoNotify: true}, ""); err != nil {
		t.Fatal(err)
	}
	request := <-c.Requests()
	if request.Kind != "confirm_start" {
		t.Fatalf("request = %q, want confirm_start", request.Kind)
	}
	if err := request.Answer(Response{Confirmed: false}); err != nil {
		t.Fatal(err)
	}
	awaitDone(t, c)
	if got := c.State(); got != StateCancelled {
		t.Fatalf("state = %s, want %s", got, StateCancelled)
	}
	if c.Checkpointed() {
		t.Fatal("a declined start wrote a pause checkpoint")
	}
	if _, err := ReadCheckpoint(f.root); err == nil {
		t.Fatal("a declined start persisted a checkpoint")
	}
}
