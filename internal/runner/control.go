package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// State describes the lifecycle of an interactive run.
type State string

const (
	StateIdle      State = "Idle"
	StateStarting  State = "Starting"
	StateRunning   State = "Running"
	StatePausing   State = "Pausing"
	StatePaused    State = "Paused"
	StateStopping  State = "Stopping"
	StateBlocked   State = "Blocked"
	StateCancelled State = "Cancelled"
	StateFinished  State = "Finished"
	StateFailed    State = "Failed"
)

// Event is a sanitized, typed observation emitted by a runner.
type Event struct {
	Type    string
	Unit    string
	Message string
	Fields  map[string]any
	// Data carries a typed observation, currently an agentio.Activity. It is
	// published to the interactive host only and never serialized into the
	// event stream.
	Data any
}

// Request is a decision the interactive owner must answer.
type Request struct {
	Kind    string
	Unit    string
	Message string
	// Fields carries typed context for the dialog, such as a StartReview.
	Fields map[string]any
	answer chan Response
}

type Response struct {
	Confirmed bool
	Note      string
}

// Control is the single command and observation point for an interactive run.
// Execute receives a context and the host so it can publish events and ask for
// human decisions without reading stdin or writing to a terminal.
type Control struct {
	mu       sync.Mutex
	state    State
	cancel   context.CancelFunc
	requests chan Request
	events   chan Event
	done     chan struct{}
	root     string
	// checkpointed records that this execution wrote a Pause checkpoint. A
	// Pausing state alone is a request, not a saved pause.
	checkpointed bool
	// startCancelled records that the interactive owner declined or cancelled
	// the pending confirmation before any work began.
	startCancelled bool
	// err is the execution error of the last run. A refusal before the first
	// event has no journal entry, so the host reads its reason here instead of
	// seeing only a failed state.
	err error
}

func NewControl(eventBuffer, requestBuffer int) *Control {
	if eventBuffer < 1 {
		eventBuffer = 1
	}
	if requestBuffer < 1 {
		requestBuffer = 1
	}
	return &Control{state: StateIdle, events: make(chan Event, eventBuffer), requests: make(chan Request, requestBuffer), done: make(chan struct{})}
}

func (c *Control) State() State             { c.mu.Lock(); defer c.mu.Unlock(); return c.state }
func (c *Control) Checkpointed() bool       { c.mu.Lock(); defer c.mu.Unlock(); return c.checkpointed }
func (c *Control) Err() error               { c.mu.Lock(); defer c.mu.Unlock(); return c.err }
func (c *Control) Events() <-chan Event     { return c.events }
func (c *Control) Requests() <-chan Request { return c.requests }
func (c *Control) Done() <-chan struct{}    { return c.done }

// Publish never waits for a slow UI. When the bounded queue is full, it drops
// the oldest observation so current state remains visible.
func (c *Control) Publish(e Event) {
	select {
	case c.events <- e:
		return
	default:
	}
	select {
	case <-c.events:
	default:
	}
	select {
	case c.events <- e:
	default:
	}
}

func (c *Control) Ask(ctx context.Context, req Request) (Response, error) {
	req.answer = make(chan Response, 1)
	select {
	case c.requests <- req:
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
	select {
	case answer := <-req.answer:
		return answer, nil
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
}

func (r Request) Answer(answer Response) error {
	if r.answer == nil {
		return errors.New("runner: request is not answerable")
	}
	select {
	case r.answer <- answer:
		return nil
	default:
		return errors.New("runner: request already answered")
	}
}

// Start runs one execution at a time. Cancellation is a pause request; Execute
// must stop child processes and return before the host reports Paused.
func (c *Control) Start(parent context.Context, execute func(context.Context, *Control) error) error {
	c.mu.Lock()
	if c.state != StateIdle && c.state != StatePaused && c.state != StateCancelled && c.state != StateFinished && c.state != StateFailed && c.state != StateBlocked {
		c.mu.Unlock()
		return errors.New("runner: a command is already active")
	}
	ctx, cancel := context.WithCancel(parent)
	c.cancel = cancel
	c.state = StateStarting
	c.checkpointed = false
	c.startCancelled = false
	c.err = nil
	c.done = make(chan struct{})
	c.mu.Unlock()
	go func() {
		err := execute(ctx, c)
		paused := c.State() == StatePausing && c.Checkpointed()
		blocked := c.State() == StateBlocked
		stopping := c.State() == StateStopping
		cancel()
		c.mu.Lock()
		cancelled := c.startCancelled
		c.err = err
		switch {
		case paused && err == nil:
			c.state = StatePaused
		case paused:
			c.state = StateFailed
		case blocked:
			c.state = StateBlocked
		case cancelled:
			c.state = StateCancelled
		case stopping:
			c.state = StateFinished
		case err != nil:
			c.state = StateFailed
		default:
			c.state = StateFinished
		}
		close(c.done)
		c.mu.Unlock()
	}()
	return nil
}

// StartCheckout acquires the same per-checkout lock as Run before starting the
// controlled execution and releases it only after Execute has stopped.
func (c *Control) StartCheckout(parent context.Context, root string, execute func(context.Context, *Control) error) error {
	release, err := runLock(root)
	if err != nil {
		return err
	}
	if err := c.Start(parent, func(ctx context.Context, host *Control) error {
		host.mu.Lock()
		host.root = root
		host.mu.Unlock()
		defer release()
		return execute(ctx, host)
	}); err != nil {
		release()
		return err
	}
	return nil
}

// StartRun starts the standard runner loop under interactive ownership.
func (c *Control) StartRun(parent context.Context, root string, args Args, skillDirectory string) error {
	return c.startRun(parent, root, args, skillDirectory, nil, true)
}

// ResumeRun continues the checkpoint saved by a Pause on the same unit and
// assignment. A checkpoint whose plan or HEAD moved is reported as a recovery
// conflict and nothing is started. A fresh process is Idle, and a live one is
// Paused, so both are accepted.
func (c *Control) ResumeRun(parent context.Context, root string, args Args, skillDirectory string) error {
	checkpoint, err := ReadCheckpoint(root)
	if err != nil {
		return fmt.Errorf("runner: no resumable checkpoint: %w", err)
	}
	if err := ValidateResumable(checkpoint); err != nil {
		c.Publish(Event{Type: "recovery_conflict", Unit: checkpoint.Unit, Message: err.Error()})
		return err
	}
	if err := ValidateCheckpoint(root, checkpoint); err != nil {
		c.Publish(Event{Type: "recovery_conflict", Unit: checkpoint.Unit, Message: err.Error()})
		return err
	}
	return c.startRun(parent, root, args, skillDirectory, &checkpoint, false)
}

func (c *Control) startRun(parent context.Context, root string, args Args, skillDirectory string, checkpoint *Checkpoint, confirm bool) error {
	return c.StartCheckout(parent, root, func(ctx context.Context, host *Control) error {
		if confirm {
			fields := map[string]any{}
			if preview, err := StartPreview(root, args); err == nil {
				fields["preview"] = preview
			} else {
				fields["preview_error"] = err.Error()
			}
			answer, err := host.Ask(ctx, Request{Kind: "confirm_start", Message: "Start the current Queue run?", Fields: fields})
			if err != nil {
				return err
			}
			if !answer.Confirmed {
				host.MarkStartCancelled()
				return nil
			}
		}
		if !host.MarkRunning() {
			return errors.New("runner: invalid start transition")
		}
		code, runErr := runControlled(ctx, root, args, skillDirectory, host, checkpoint, true)
		if code == 3 {
			host.MarkBlocked()
			return nil
		}
		if runErr != nil {
			if host.Checkpointed() && errors.Is(runErr, context.Canceled) {
				return nil
			}
			return runErr
		}
		if code != 0 && code != 1 {
			return fmt.Errorf("runner: run exited with code %d", code)
		}
		return nil
	})
}

// MarkRunning marks the accepted confirmation and the beginning of work.
func (c *Control) MarkRunning() bool { return c.setState(StateStarting, StateRunning) }

// MarkStartCancelled records that the owner declined the pending confirmation.
// Such a start finishes as Cancelled, not as a completed run.
func (c *Control) MarkStartCancelled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != StateStarting {
		return false
	}
	c.startCancelled = true
	return true
}

func (c *Control) MarkBlocked() bool { return c.setState(StateRunning, StateBlocked) }
func (c *Control) setState(from, to State) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != from {
		return false
	}
	c.state = to
	return true
}

func (c *Control) Pause() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != StateRunning {
		return errors.New("runner: pause requires a running run")
	}
	c.state = StatePausing
	c.cancel()
	return nil
}

// checkpoint persists the last observed execution boundary before a canceled
// agent call is reported as Paused.
func (c *Control) checkpoint(root string, cp Checkpoint) error {
	if err := WriteCheckpoint(root, cp); err != nil {
		return err
	}
	c.mu.Lock()
	c.checkpointed = true
	c.mu.Unlock()
	return nil
}

// CancelStart cancels a pending interactive confirmation without starting work.
func (c *Control) CancelStart() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != StateStarting {
		return errors.New("runner: no start confirmation is pending")
	}
	c.startCancelled = true
	c.state = StateStopping
	c.cancel()
	return nil
}

func (c *Control) StopAfterUnit() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != StateRunning {
		return errors.New("runner: stop-after-unit requires a running run")
	}
	c.state = StateStopping
	if c.root != "" {
		path, err := stopFile(c.root)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o666)
		if err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	c.Publish(Event{Type: "stop_after_unit"})
	return nil
}

// StopNow interrupts a running execution without writing a Pause checkpoint.
// The host reports it as a finished run, not as a resumable pause.
func (c *Control) StopNow() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != StateRunning {
		return errors.New("runner: stop now requires a running run")
	}
	c.state = StateStopping
	c.cancel()
	return nil
}

func (c *Control) Resume(parent context.Context, execute func(context.Context, *Control) error) error {
	if c.State() != StatePaused {
		return errors.New("runner: resume requires a paused run")
	}
	return c.Start(parent, execute)
}
