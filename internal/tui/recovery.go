package tui

import (
	"fmt"
	"strings"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/runner"
)

// refreshRecovery reads the checkpoint and the newest journal after a plan
// load. The first successful load opens the recovery dialog when a checkpoint
// exists; later reloads keep the recovery visible without replacing whatever
// dialog or notice the current action owns.
func (m Model) refreshRecovery() Model {
	if m.runIsActive() || (m.controller != nil && !m.controllerDone) {
		return m
	}
	loader := m.recoveryLoad
	if loader == nil {
		loader = runner.LoadRecovery
	}
	recovery, err := loader(m.root)
	if err != nil {
		// An unreadable recovery state is not a reason to hide the tree.
		m.recovery = nil
		return m
	}
	first := !m.recoveryPrompted
	if recovery.Checkpoint == nil && recovery.LastRun == nil {
		m.recovery = nil
		m.recoveryPrompted = true
		return m
	}
	m.recovery = &recovery
	if !first || recovery.Checkpoint == nil {
		m.recoveryPrompted = true
		return m
	}
	if m.dialog != dialogNone {
		// Another decision already owns the keyboard; a later load offers the
		// saved run once the dialog is gone. The tree and R still use it.
		return m
	}
	m.recoveryPrompted = true
	return m.openRecoveryDialog()
}

// openRecoveryDialog shows the saved run, its last outcome and the assignment
// table, and asks for Continue or a new run.
func (m Model) openRecoveryDialog() Model {
	recovery := m.recovery
	if recovery == nil || recovery.Checkpoint == nil {
		return m
	}
	lines, keys := m.recoveryDialogLines(*recovery)
	return m.openDialog(dialogRecover, "Recover the saved run", lines, keys)
}

func (m Model) recoveryDialogLines(recovery runner.Recovery) ([]string, []string) {
	checkpoint := recovery.Checkpoint
	plan := m.planByID(checkpoint.Plan)
	planLabel := checkpoint.Plan
	unitLabel := checkpoint.Unit
	if plan != nil {
		planLabel = plan.ID + " " + plan.Title
		if unit := plan.unit(checkpoint.Unit); unit != nil && checkpoint.Unit != "" {
			unitLabel = unit.ID + " " + unit.Title
		}
	}
	lines := []string{"Plan: " + planLabel}
	if unitLabel != "" {
		lines = append(lines, "Unit: "+unitLabel)
	}
	stage := runner.StageName(checkpoint.Stage)
	if recovery.PendingCommit {
		stage = "the done/commit transaction (commit pending)"
	}
	lines = append(lines, "Stage: "+stage)
	if recovery.LastRun != nil {
		lines = append(lines, lastRunText(*recovery.LastRun))
	}
	var keys []string
	switch {
	case recovery.Completed:
		lines = append(lines, "", fmt.Sprintf("%s is already complete; there is no interrupted work to continue.", checkpoint.Unit))
		keys = []string{"s starts a new run · Esc views the workspace"}
	case recovery.Problem != "":
		lines = append(lines, "", "Cannot continue this checkpoint: "+recovery.Problem)
		keys = []string{"s starts a new run · Esc views the workspace"}
	case recovery.PendingCommit:
		lines = append(lines, "", "R finishes the pending commit with the existing cycle; the unit is not run again.")
		keys = []string{"Enter/R recovers the pending commit · s starts a new run · Esc views the workspace"}
	default:
		lines = append(lines, "", "R continues the interrupted unit from its checkpoint; s starts a new run with the plan's settings.")
		keys = []string{"Enter/R continues · s starts a new run · Esc views the workspace"}
	}
	lines = append(lines, m.recoveryAssignments(checkpoint.Plan)...)
	return lines, keys
}

// recoveryAssignments renders the effective assignment table of the saved
// plan. The saved overlay is re-resolved against the plan on disk, so the
// dialog shows what the next start will actually use.
func (m Model) recoveryAssignments(planID string) []string {
	configuration, err := runner.LoadConfiguration(m.root)
	if err != nil {
		return nil
	}
	plan := m.planByID(planID)
	if plan == nil {
		if configuration.Effective != nil && configuration.Effective.Plan == planID {
			return runner.AssignmentLines(configuration.Effective)
		}
		return nil
	}
	var agentConfig map[string]any
	if loader := m.modelsConfig; loader != nil {
		agentConfig, _ = loader(plan.Doc, agents.RunArgs{})
	}
	effective, err := runner.EffectiveAssignments(plan.Doc, agents.RunArgs{}, agentConfig, configuration)
	if err != nil {
		return nil
	}
	return runner.AssignmentLines(effective)
}

// planByID finds a loaded plan in either Queue or Frozen.
func (m Model) planByID(id string) *PlanView {
	for _, plan := range m.snapshot.Queue {
		if plan.ID == id {
			return plan
		}
	}
	for _, plan := range m.snapshot.Frozen {
		if plan.ID == id {
			return plan
		}
	}
	return nil
}

func lastRunText(summary runner.RunSummary) string {
	reason := summary.Reason
	if reason == "" {
		reason = "no run_end recorded"
	}
	text := fmt.Sprintf("Last run: %s — %s", summary.RunID, reason)
	if summary.Reason != "" {
		text += fmt.Sprintf(" (exit %d)", summary.ExitCode)
	}
	if summary.Paused {
		text += ", paused"
	}
	if summary.Unit != "" && summary.UnitStatus != "" {
		text += fmt.Sprintf("; %s %s", summary.Unit, summary.UnitStatus)
	}
	return text
}

// recoveryRefusal names why Continue is refused, or returns an empty string
// when it may proceed. Without a saved classification the runner's own
// checkpoint validation decides, as it did before recovery was shown.
func (m Model) recoveryRefusal() string {
	recovery := m.recovery
	if recovery == nil {
		return ""
	}
	if recovery.Checkpoint == nil {
		return "no saved checkpoint to resume; press s to start a new run"
	}
	if recovery.Problem != "" {
		return "cannot resume the saved run: " + recovery.Problem + "; press s to start a new run"
	}
	if recovery.Completed {
		return fmt.Sprintf("%s is already complete; press s to start a new run", recovery.Checkpoint.Unit)
	}
	return ""
}

// recoveryNotice is the short status-line text left when the recovery dialog
// is dismissed without a decision.
func (m Model) recoveryNotice() string {
	recovery := m.recovery
	if recovery == nil || recovery.Checkpoint == nil {
		return "viewing the workspace"
	}
	if notice := m.recoveryRefusal(); notice != "" {
		return notice
	}
	stage := runner.StageName(recovery.Checkpoint.Stage)
	if recovery.PendingCommit {
		stage = "a pending commit"
	}
	return fmt.Sprintf("saved run: %s interrupted during %s; R continues, s starts a new run", recovery.Checkpoint.Unit, stage)
}

// recoverySection is the Recovery block of the interrupted unit's card.
func (m Model) recoverySection(unit string) []string {
	recovery := m.recovery
	if recovery == nil || recovery.Checkpoint == nil || recovery.Checkpoint.Unit != unit {
		return nil
	}
	stage := runner.StageName(recovery.Checkpoint.Stage)
	if recovery.PendingCommit {
		stage = "the done/commit transaction (commit pending)"
	}
	lines := []string{"Stage: " + stage}
	if recovery.LastRun != nil {
		lines = append(lines, lastRunText(*recovery.LastRun))
	}
	if notice := m.recoveryRefusal(); notice != "" {
		lines = append(lines, notice)
		return lines
	}
	lines = append(lines, "R continues from the checkpoint; s starts a new run with the plan's settings.")
	return lines
}

// recoverySummaryLine names the saved run in the status line while the
// workspace is open.
func (m Model) recoverySummaryLine() string {
	recovery := m.recovery
	if recovery == nil || recovery.Checkpoint == nil {
		return ""
	}
	flow := strings.TrimSpace(recovery.Checkpoint.Unit)
	if flow == "" {
		flow = recovery.Checkpoint.Plan
	}
	if recovery.Completed {
		return "recovery:" + flow + " complete"
	}
	if recovery.PendingCommit {
		return "recovery:" + flow + " commit-pending"
	}
	if recovery.Problem != "" {
		return "recovery:" + flow + " conflict"
	}
	return "recovery:" + flow
}
