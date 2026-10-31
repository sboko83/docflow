package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestControlCommandsAreExclusive(t *testing.T) {
	c := NewControl(2, 1)
	started, finish := make(chan struct{}), make(chan struct{})
	if err := c.Start(context.Background(), func(ctx context.Context, host *Control) error {
		close(started)
		<-finish
		return nil
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
	if got := c.State(); got != StateFinished {
		t.Fatalf("state = %s, want %s", got, StateFinished)
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
