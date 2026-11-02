package tui

import (
	"context"
	"sync"

	"bsv-docflow/internal/runner"
)

// Prompt is one decision the interactive host must answer. It carries the
// typed fields the runner attached, such as a runner.StartReview, and an
// answer function that records the response exactly once.
type Prompt struct {
	Kind    string
	Unit    string
	Message string
	Fields  map[string]any
	answer  func(confirmed bool, note string)
}

// Answer sends the decision to the runner. A prompt without an answer path
// (a test fixture) accepts the call and does nothing.
func (p Prompt) Answer(confirmed bool, note string) {
	if p.answer != nil {
		p.answer(confirmed, note)
	}
}

// Controller is the part of the shared runner host the workspace drives. It is
// polled from the UI loop so no Bubble Tea command blocks on a runner channel.
type Controller interface {
	Start() error
	Resume() error
	Pause() error
	CancelStart() error
	StopAfterUnit() error
	StopNow() error
	State() runner.State
	Checkpointed() bool
	NextEvent() (runner.Event, bool)
	NextPrompt() (Prompt, bool)
	Finished() bool
	Err() error
	Close()
}

// RunLimits are the controlled run's attempts per unit and replans per phase.
type RunLimits struct {
	Attempts int
	Replans  int
}

// DefaultRunLimits are the run limits of the terminal workspace when the
// command line does not override them.
func DefaultRunLimits() RunLimits { return RunLimits{Attempts: 3, Replans: 3} }

// Host owns one runner.Control for the checkout and applies the run limits of
// the terminal workspace. The plan keeps its own executor, permission and
// replan settings.
type Host struct {
	root, skills string
	args         runner.Args
	ctx          context.Context
	cancel       context.CancelFunc
	control      *runner.Control

	mu     sync.Mutex
	launch error
}

// NewHost builds an idle host for root. The checkout lock is not taken until
// the run actually starts. Without an explicit limit it uses the workspace
// defaults of three attempts and three replans.
func NewHost(root, skills string, limits ...RunLimits) *Host {
	limit := DefaultRunLimits()
	if len(limits) > 0 {
		limit = limits[0]
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Host{
		root: root, skills: skills,
		args:    runner.Args{Attempts: limit.Attempts, Replans: limit.Replans, NoNotify: true},
		ctx:     ctx,
		cancel:  cancel,
		control: runner.NewControl(256, 4),
	}
}

func (h *Host) setErr(err error) {
	h.mu.Lock()
	h.launch = err
	h.mu.Unlock()
}

// Start begins the current Queue run. StartCheckout inside the control takes
// the shared checkout lock, so a live foreign runner refuses this call.
func (h *Host) Start() error {
	err := h.control.StartRun(h.ctx, h.root, h.args, h.skills)
	if err != nil {
		h.setErr(err)
	}
	return err
}

// Resume validates the checkpoint and continues the interrupted unit without
// asking the start confirmation again.
func (h *Host) Resume() error {
	err := h.control.ResumeRun(h.ctx, h.root, h.args, h.skills)
	if err != nil {
		h.setErr(err)
	}
	return err
}

func (h *Host) Pause() error         { return h.control.Pause() }
func (h *Host) CancelStart() error   { return h.control.CancelStart() }
func (h *Host) StopAfterUnit() error { return h.control.StopAfterUnit() }
func (h *Host) StopNow() error       { return h.control.StopNow() }
func (h *Host) State() runner.State  { return h.control.State() }
func (h *Host) Checkpointed() bool   { return h.control.Checkpointed() }

func (h *Host) NextEvent() (runner.Event, bool) {
	select {
	case event := <-h.control.Events():
		return event, true
	default:
		return runner.Event{}, false
	}
}

func (h *Host) NextPrompt() (Prompt, bool) {
	select {
	case request := <-h.control.Requests():
		prompt := Prompt{
			Kind: request.Kind, Unit: request.Unit, Message: request.Message, Fields: request.Fields,
			answer: func(confirmed bool, note string) {
				_ = request.Answer(runner.Response{Confirmed: confirmed, Note: note})
			},
		}
		return prompt, true
	default:
		return Prompt{}, false
	}
}

func (h *Host) Finished() bool {
	select {
	case <-h.control.Done():
		return true
	default:
		return false
	}
}

func (h *Host) Err() error {
	h.mu.Lock()
	launch := h.launch
	h.mu.Unlock()
	if launch != nil {
		return launch
	}
	// A run that fails after authentication but before the first event has no
	// journal entry; its reason lives on the control, not on the launch.
	return h.control.Err()
}

func (h *Host) Close() {
	h.cancel()
}
