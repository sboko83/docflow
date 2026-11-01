package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/runner"
)

type dialogKind int

const (
	dialogNone dialogKind = iota
	dialogStart
	dialogExit
	dialogHuman
)

type hostPollMsg struct{}

type lifecycleDoneMsg struct {
	action string
	planID string
	result cycle.Result
}

func pollSoon() tea.Cmd {
	return tea.Tick(40*time.Millisecond, func(time.Time) tea.Msg { return hostPollMsg{} })
}

// eventReloadsPlan names observations after which the tree can show a new
// unit status or a changed Queue and plan list.
func eventReloadsPlan(kind string) bool {
	switch kind {
	case "run_start", "unit_start", "unit_end", "commit", "verify", "replan", "close", "run_end":
		return true
	}
	return false
}

// refreshLock records a live foreign runner. The workspace keeps reading the
// plans, but refuses to start or change their lifecycle until it is gone. An
// unreadable lock is unknown, never a free checkout: control stays refused
// while viewing remains available.
func (m *Model) refreshLock() {
	if m.lockCheck == nil {
		m.lockCheck = runner.CheckoutLockOwner
	}
	pid, held, err := m.lockCheck(m.root)
	m.lockErr = nil
	if err != nil {
		m.lockPID, m.lockHeld, m.lockErr = 0, false, err
		return
	}
	if !held || pid == os.Getpid() {
		m.lockPID, m.lockHeld = 0, false
		return
	}
	m.lockPID, m.lockHeld = pid, true
}

// lockRefusal names why control is refused, or reports that it is allowed.
// Viewing plans and history never consults the lock.
func (m Model) lockRefusal(purpose string) (string, bool) {
	if m.lockHeld {
		return fmt.Sprintf("another runner is active (pid %d); viewing is allowed, %s", m.lockPID, purpose), true
	}
	if m.lockErr != nil {
		return fmt.Sprintf("cannot read the checkout lock: %s; viewing is allowed, %s", m.lockErr, purpose), true
	}
	return "", false
}

func (m Model) runIsActive() bool {
	switch m.runState {
	case runner.StateStarting, runner.StateRunning, runner.StatePausing, runner.StateStopping:
		return true
	}
	return false
}

func (m Model) selectedPlan() *PlanView {
	selected, ok := m.selectedRow()
	if !ok {
		return nil
	}
	return selected.plan
}

// startRun starts a fresh run or resumes the checkpoint. The runner asks for
// its own start confirmation through the prompt channel; the workspace never
// answers it on the person's behalf. The last result reports whether a
// controller was created: a caller that also did something else, such as an
// unblock, must not report a start that never happened.
func (m Model) startRun(resume bool) (Model, tea.Cmd, bool) {
	if m.runIsActive() || (m.controller != nil && !m.controllerDone) {
		m.notice = "a run is already active; wait for it or stop it"
		return m, nil, false
	}
	if m.controller != nil && m.controllerDone {
		m.controller.Close()
		m.controller = nil
	}
	// The lock is re-read at the action, not only at load: another runner can
	// appear while the workspace is open.
	m.refreshLock()
	if notice, refused := m.lockRefusal("starting is not"); refused {
		m.notice = notice
		return m, nil, false
	}
	if resume {
		// ResumeRun validates the checkpoint and reports recovery conflicts;
		// a second check here would duplicate its decision.
	} else if len(m.snapshot.Queue) == 0 {
		m.notice = "nothing to start: Queue is empty"
		return m, nil, false
	}
	factory := m.newController
	if factory == nil {
		factory = func(root, skills string) Controller { return NewHost(root, skills) }
	}
	controller := factory(m.root, m.skillDirectory)
	if controller == nil {
		m.notice = "cannot create the runner host"
		return m, nil, false
	}
	var err error
	if resume {
		err = controller.Resume()
	} else {
		err = controller.Start()
	}
	if err != nil {
		controller.Close()
		m.notice = err.Error()
		return m, nil, false
	}
	m.controller = controller
	m.controllerDone = false
	m.runState = controller.State()
	m.runErr = ""
	m.runStopped = false
	m.checkpointed = false
	m.blockedUnit, m.blockedReason = "", ""
	m.blockedPending = false
	if resume {
		m.notice = "resuming from checkpoint"
	} else {
		m.notice = "starting the current Queue run"
	}
	return m, pollSoon(), true
}

func (m Model) pollController() (Model, tea.Cmd) {
	controller := m.controller
	if controller == nil {
		return m, nil
	}
	reload := false
	for i := 0; i < 256; i++ {
		event, ok := controller.NextEvent()
		if !ok {
			break
		}
		if event.Type == "start_failure" {
			// The refusal happened before run_start, so no run_end carries
			// it: the host-only event is the only source of the reason.
			m.runErr = event.Message
		}
		if event.Type == "run_end" {
			switch reason := strValue(event.Fields["reason"]); reason {
			case "", "paused":
			case "stopped":
				// A controlled Stop now is an action the user took, not a
				// runner failure: the status shows it without an error text.
				m.runStopped = true
			default:
				m.runErr = reason
			}
		}
		m = m.applyEvent(event)
		reload = reload || eventReloadsPlan(event.Type)
	}
	for m.pendingPrompt == nil {
		prompt, ok := controller.NextPrompt()
		if !ok {
			break
		}
		m = m.handlePrompt(prompt)
	}
	if !m.controllerDone {
		m.runState = controller.State()
		m.checkpointed = controller.Checkpointed()
	}
	if !m.controllerDone && controller.Finished() {
		m.controllerDone = true
		m.runState = controller.State()
		m.checkpointed = controller.Checkpointed()
		if err := controller.Err(); err != nil && !m.runStopped {
			m.runErr = err.Error()
		}
		m.handleFinished()
		reload = true
		if m.exitAfterRun {
			controller.Close()
			return m, tea.Quit
		}
	}
	var commands []tea.Cmd
	if reload {
		commands = append(commands, loadCmd(m.load))
	}
	if !m.controllerDone && (m.runIsActive() || m.pendingPrompt != nil) {
		commands = append(commands, pollSoon())
	}
	return m, tea.Batch(commands...)
}

func (m *Model) handleFinished() {
	// Our own run released the lock when it stopped; re-read it so the status
	// line and the control guard never disagree.
	m.refreshLock()
	switch m.runState {
	case runner.StateBlocked:
		// `docflow block` writes `[!]` and its Handoff item while the runner is
		// still stopping, so the snapshot loaded before the stop cannot contain
		// them. Defer the decision to the reload pollController requests next;
		// reading the plan here would add a second loader to the UI thread.
		m.blockedPending = true
		m.blockedUnit, m.blockedReason = "", ""
		m.notice = "the run stopped on a block; reloading the plan"
	case runner.StatePaused:
		if m.checkpointed {
			m.notice = "paused; Resume continues the interrupted unit from its checkpoint"
		} else {
			m.notice = "the run stopped without a resumable checkpoint"
		}
	case runner.StateFailed:
		if m.runErr == "" {
			m.runErr = "the run failed"
		}
		m.notice = "run failed: " + m.runErr
	case runner.StateCancelled:
		// No work started, so the decline or cancel notice already set by the
		// dialog or the exit path is the outcome; "run finished" is wrong.
		if m.notice == "" {
			m.notice = "the start was cancelled; nothing ran"
		}
	case runner.StateFinished:
		if m.runStopped {
			m.notice = "run stopped"
		} else if m.runErr != "" {
			m.notice = "run finished: " + m.runErr
		} else {
			m.notice = "run finished"
		}
	}
}

func (m Model) resolveBlocked() Model {
	unit, reason, needs, ok := m.blockedInfo()
	if !ok || needs == "" {
		// A unit blocked by hand has no Handoff item naming who lifts it.
		m.notice = "the run stopped on a block"
		return m
	}
	m.blockedUnit, m.blockedReason = unit, reason
	if needs != "human" {
		m.notice = fmt.Sprintf("%s is blocked (needs %s): %s — replan or return it to the CLI", unit, needs, reason)
		return m
	}
	if reason == "" {
		reason = "the plan records no reason"
	}
	m.dialog = dialogHuman
	m.dialogTitle = unit + " is blocked (needs human)"
	m.dialogLines = []string{reason, "", "Continue runs the existing unblock command with your note."}
	m.dialogInput, m.dialogNote = "", true
	return m
}

func (m Model) blockedInfo() (unit, reason, needs string, ok bool) {
	for _, plan := range append(append([]*PlanView{}, m.snapshot.Queue...), m.snapshot.Frozen...) {
		for _, item := range plan.Doc.Units {
			if item.Status != "!" {
				continue
			}
			needs, reason := cycle.BlockedNeeds(item)
			return item.ID, reason, needs, true
		}
	}
	return "", "", "", false
}

func (m Model) handlePrompt(prompt Prompt) Model {
	if m.pendingPrompt != nil {
		prompt.Answer(false, "")
		return m
	}
	p := prompt
	m.pendingPrompt = &p
	switch prompt.Kind {
	case "confirm_start":
		m.dialog = dialogStart
		m.dialogTitle = "Start the current Queue run?"
		m.dialogLines = startDialogLines(prompt)
		m.dialogInput, m.dialogNote = "", false
	default:
		m.dialog = dialogStart
		m.dialogTitle = prompt.Message
		line := prompt.Message
		if prompt.Unit != "" {
			line = prompt.Unit + ": " + line
		}
		m.dialogLines = []string{line, "", "Enter/y confirms · Esc/n declines"}
	}
	return m
}

func startDialogLines(prompt Prompt) []string {
	fields := prompt.Fields
	if errText := strValue(fields["preview_error"]); errText != "" {
		return []string{"Cannot read the current selection:", errText, "", "Enter/y tries anyway · Esc/n cancels"}
	}
	preview, ok := fields["preview"].(runner.StartReview)
	if !ok {
		return []string{prompt.Message, "", "Enter/y starts · Esc/n cancels"}
	}
	lines := []string{preview.Summary()}
	if preview.Permissions != "" {
		line := "permissions: " + preview.Permissions
		if preview.Full {
			line += " — the agent runs without a sandbox"
		}
		lines = append(lines, line)
	}
	if len(preview.Prerequisites) > 0 {
		lines = append(lines, "", "prerequisites:")
		for _, item := range preview.Prerequisites {
			lines = append(lines, "  - "+item)
		}
	}
	lines = append(lines, "", "Enter/y starts · Esc/n cancels; declining runs nothing")
	return lines
}

func (m Model) answerPrompt(confirmed bool, note string) Model {
	if m.pendingPrompt == nil {
		return m
	}
	m.pendingPrompt.Answer(confirmed, note)
	m.pendingPrompt = nil
	return m
}

func (m Model) handleDialogKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	key := msg.String()
	switch m.dialog {
	case dialogStart:
		switch key {
		case "enter", "y":
			m = m.answerPrompt(true, "")
			m.dialog = dialogNone
			m.notice = "start confirmed"
			return m, pollSoon()
		case "esc", "n":
			m = m.answerPrompt(false, "")
			m.dialog = dialogNone
			m.notice = "start declined; nothing ran"
			return m, pollSoon()
		}
		return m, nil
	case dialogExit:
		switch key {
		case "enter", "y":
			m.dialog = dialogNone
			return m.stopAndExit()
		case "esc", "n":
			m.dialog = dialogNone
			m.notice = "staying with the live run"
			return m, pollSoon()
		}
		return m, nil
	case dialogHuman:
		switch key {
		case "enter":
			unit := m.blockedUnit
			note := strings.TrimSpace(m.dialogInput)
			if unit == "" {
				m.dialog = dialogNone
				return m, nil
			}
			// The unblock runs through cycle.Self, which takes no lock of its
			// own: this guard is the only check, and it must read the lock now,
			// not at load. A refusal keeps the dialog and the typed note.
			m.refreshLock()
			if notice, refused := m.lockRefusal("the unblock is refused"); refused {
				m.notice = notice
				return m, nil
			}
			m.dialog = dialogNone
			lifecycle := m.lifecycle
			root := m.root
			return m, func() tea.Msg {
				result := lifecycle(root, "unblock", unit, "--note="+note)
				return lifecycleDoneMsg{action: "unblock", planID: unit, result: result}
			}
		case "esc":
			m.dialog = dialogNone
			m.notice = "left " + m.blockedUnit + " blocked"
			return m, nil
		case "backspace":
			runes := []rune(m.dialogInput)
			if len(runes) > 0 {
				m.dialogInput = string(runes[:len(runes)-1])
			}
			return m, nil
		default:
			if msg.Text != "" && msg.Mod == 0 {
				m.dialogInput += msg.Text
			}
			return m, nil
		}
	}
	return m, nil
}

func (m Model) requestQuit() (Model, tea.Cmd) {
	if m.runIsActive() || (m.controller != nil && !m.controllerDone) {
		m.dialog = dialogExit
		m.dialogTitle = "A run is still active"
		m.dialogLines = []string{
			"Enter/y stops the run at a stable boundary, writes its checkpoint and exits.",
			"Esc/n stays with the live run.",
		}
		return m, nil
	}
	if m.controller != nil {
		m.controller.Close()
		m.controller = nil
	}
	return m, tea.Quit
}

// stopWaitTimeout bounds how long a terminal signal waits for the run to
// reach a stable boundary and write its Pause checkpoint. The pause itself
// never counts against the agent timeout; the bound only stops a wedged
// runner from keeping the process alive. Tests shorten it.
var stopWaitTimeout = 60 * time.Second

// stopAfterSignal stops a run that is still live when the program returned.
// It mirrors stopAndExit but waits synchronously: the process must not exit
// before the checkpoint is written and the checkout lock is released.
func (m Model) stopAfterSignal() error {
	controller := m.controller
	if controller == nil {
		return nil
	}
	defer controller.Close()
	state := controller.State()
	if state == runner.StatePaused && !controller.Checkpointed() {
		return fmt.Errorf("docflow tui: the run is Paused without a written checkpoint")
	}
	switch state {
	case runner.StateRunning:
		if err := controller.Pause(); err != nil {
			// The run left Running before the signal was applied; nothing is live.
			return nil
		}
	case runner.StateStarting:
		// No unit started yet: cancel the pending confirmation without a
		// checkpoint, which is the correct record of an unused start.
		if err := controller.CancelStart(); err != nil {
			// The start confirmation was accepted just before the signal and
			// the run is now live: pause that run instead of leaving it behind.
			_ = controller.Pause()
		}
	case runner.StatePausing, runner.StateStopping:
		// A stop is already on its way to the stable boundary.
	default:
		return nil
	}
	deadline := time.Now().Add(stopWaitTimeout)
	for !controller.Finished() {
		if time.Now().After(deadline) {
			return fmt.Errorf("docflow tui: the run did not stop within %s", stopWaitTimeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if controller.State() == runner.StatePaused && !controller.Checkpointed() {
		return fmt.Errorf("docflow tui: the run stopped as Paused without writing a checkpoint")
	}
	if controller.State() == runner.StateFailed {
		if err := controller.Err(); err != nil {
			return fmt.Errorf("docflow tui: the run failed while stopping: %w", err)
		}
	}
	return nil
}

func (m Model) stopAndExit() (Model, tea.Cmd) {
	m.exitAfterRun = true
	if m.controller == nil {
		return m, tea.Quit
	}
	if m.pendingPrompt != nil {
		m = m.answerPrompt(false, "")
	}
	switch m.controller.State() {
	case runner.StateRunning:
		if err := m.controller.Pause(); err != nil {
			m.notice = err.Error()
		} else {
			m.runState = runner.StatePausing
			m.notice = "pausing; the checkpoint is written before the exit"
		}
	case runner.StateStarting:
		// Cancel the start whether or not a prompt arrived: s and q can reach
		// the workspace in one write before the runner publishes it. StopNow
		// requires a running run and would be swallowed here.
		if err := m.controller.CancelStart(); err != nil {
			if m.controller.State() == runner.StateRunning {
				// The confirmation won the race and the run is live: pause it
				// instead of leaving it and its checkout lock behind.
				if pauseErr := m.controller.Pause(); pauseErr != nil {
					m.notice = pauseErr.Error()
				} else {
					m.runState = runner.StatePausing
					m.notice = "the start won the race; pausing; the checkpoint is written before the exit"
				}
			} else {
				m.runState = m.controller.State()
			}
		} else {
			m.runState = runner.StateStopping
			m.notice = "cancelling the start; nothing runs"
		}
	case runner.StatePausing, runner.StateStopping:
		m.notice = "stopping; waiting for the stable boundary"
	}
	if m.controller.Finished() {
		m.controller.Close()
		return m, tea.Quit
	}
	return m, pollSoon()
}

func (m Model) lifecycleKey(action string) (Model, tea.Cmd) {
	if m.runIsActive() || (m.controller != nil && !m.controllerDone) {
		m.notice = "stop the live run before changing the plan lifecycle"
		return m, nil
	}
	// lifecycle runs through cycle.Self, which takes no lock itself: this
	// guard is the only check, so it must read the lock at the action.
	m.refreshLock()
	if notice, refused := m.lockRefusal("lifecycle changes are refused"); refused {
		m.notice = notice
		return m, nil
	}
	plan := m.selectedPlan()
	if plan == nil {
		m.notice = "select a plan first"
		return m, nil
	}
	switch action {
	case "activate":
		if plan.Status != "draft" {
			m.notice = fmt.Sprintf("%s is %s, not a draft", plan.ID, plan.Status)
			return m, nil
		}
	case "freeze":
		if plan.Status == "draft" || !m.inQueue(plan.ID) {
			m.notice = fmt.Sprintf("%s must be a queued, non-draft plan to freeze", plan.ID)
			return m, nil
		}
	case "thaw":
		if !m.inFrozen(plan.ID) {
			m.notice = fmt.Sprintf("%s is not in Frozen", plan.ID)
			return m, nil
		}
	}
	lifecycle := m.lifecycle
	root, id := m.root, plan.ID
	m.notice = action + " " + id + "..."
	return m, func() tea.Msg {
		return lifecycleDoneMsg{action: action, planID: id, result: lifecycle(root, action, id)}
	}
}

func (m Model) inQueue(id string) bool {
	for _, plan := range m.snapshot.Queue {
		if plan.ID == id {
			return true
		}
	}
	return false
}

func (m Model) inFrozen(id string) bool {
	for _, plan := range m.snapshot.Frozen {
		if plan.ID == id {
			return true
		}
	}
	return false
}

func (m Model) applyLifecycle(msg lifecycleDoneMsg) (Model, tea.Cmd) {
	if msg.result.Code != 0 {
		text := strings.TrimSpace(msg.result.Stderr)
		if text == "" {
			text = strings.TrimSpace(msg.result.Stdout)
		}
		if text == "" {
			text = fmt.Sprintf("exit %d", msg.result.Code)
		}
		m.notice = fmt.Sprintf("%s %s failed: %s", msg.action, msg.planID, firstLine(text))
		return m, nil
	}
	switch msg.action {
	case "close":
		m.notice = m.closeNotice(msg.planID, msg.result)
	case "unblock":
		m.blockedUnit, m.blockedReason = "", ""
		reloaded, cmd, started := m.startRun(false)
		if started {
			// The start ran only after the unblock, so the note names both. A
			// refused start keeps its own notice: overwriting it would report
			// a run that never began, and the reload keeps whatever text this
			// branch leaves in place.
			reloaded.notice = fmt.Sprintf("%s unblocked; starting the run again", msg.planID)
		}
		return reloaded, tea.Batch(cmd, loadCmd(m.load))
	default:
		m.notice = fmt.Sprintf("%s %s: %s", msg.action, msg.planID, firstLine(strings.TrimSpace(msg.result.Stdout)))
	}
	return m, loadCmd(m.load)
}

var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// closeNotice reports the archive path and says plainly when the template's
// Outcome still needs the person's text. A freshly created archive is not a
// finished record just because the file exists.
func (m Model) closeNotice(planID string, result cycle.Result) string {
	rel := firstLine(strings.TrimSpace(result.Stdout))
	if rel == "" {
		return "closed " + planID
	}
	path := filepath.Join(m.root, filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err == nil && archiveSectionEmpty(string(data), "Outcome") {
		return fmt.Sprintf("closed %s; the archive needs your Outcome text: %s", planID, rel)
	}
	return "closed " + planID + ": " + rel
}

func archiveSectionEmpty(text, name string) bool {
	lines := strings.Split(text, "\n")
	in := false
	for _, raw := range lines {
		line := htmlComment.ReplaceAllString(raw, "")
		trimmed := strings.TrimSpace(line)
		if !in {
			if strings.HasPrefix(trimmed, "## "+name) {
				in = true
			}
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			return true
		}
		if trimmed != "" {
			return false
		}
	}
	return in
}
