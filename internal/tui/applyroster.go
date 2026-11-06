package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/roster"
)

// The apply dialog is the roster screen's write of a plan header: it shows
// the difference between the header and the roster under the active
// variant, and Enter runs the same roster.Apply the CLI runs. It is opened
// from the Проект screen and from the Ростер screen; both write the plan
// selected in the project tree, because the roster screen has no plan list
// of its own.

// planDrift is the difference between the active roster variant and the
// header of a plan. The plan card warns about it before the person opens
// the dialog: a variant in force over a stale header means a run takes
// routes nobody applied.
type planDrift struct {
	plan    string
	variant string
	keys    int
}

// rosterWarmup reads the roster in the background once the plans are
// loaded. The loader is a read only: the plan card can then compare the
// header with the active variant without waiting for the roster screen,
// and the screen opens with its rows. A failed load stays a failure on the
// roster screen instead of retrying under every reload.
func (m *Model) rosterWarmup() tea.Cmd {
	if m.rosterPhase != rosterIdle || m.rosterPending {
		return nil
	}
	if m.rosterLoad == nil {
		m.rosterLoad = roster.LoadWorkspace
	}
	m.rosterPending = true
	return loadRosterCmd(m.rosterLoad, m.rosterPath, m.root)
}

// refreshDrift compares the header of the selected plan row with the roster
// under the active variant: the same difference the apply dialog shows.
// Only a plan row carries the warning; a phase or unit card keeps its own
// contract.
func (m Model) refreshDrift() Model {
	m.drift = planDrift{}
	if m.roster == nil || m.roster.Preset == "" {
		return m
	}
	selected, ok := m.selectedRow()
	if !ok || selected.kind != rowPlan || selected.plan == nil {
		return m
	}
	switch selected.plan.Status {
	case "draft", "active", "frozen":
	default:
		return m
	}
	report, err := m.roster.ExplainPlan(selected.plan.ID)
	if err != nil || len(report.Changes) == 0 {
		return m
	}
	m.drift = planDrift{plan: selected.plan.ID, variant: m.roster.Preset, keys: len(report.Changes)}
	return m
}

// openApplyDialog opens the apply dialog of the selected plan: the plan, the
// active variant, the keys apply would rewrite and the warnings of the plan
// roles. A refusal — a live run, a foreign runner, a missing roster, a plan
// apply cannot read — is a line in the same dialog, so the reason stays on
// screen together with what would be written.
func (m Model) openApplyDialog() Model {
	plan := m.selectedPlan()
	if plan == nil {
		m.notice = "select a plan first: A applies the roster to the plan selected in the tree"
		return m
	}
	switch plan.Status {
	case "draft", "active", "frozen":
	default:
		m.notice = fmt.Sprintf("%s is %s, roster apply wants a draft, active or frozen plan", plan.ID, plan.Status)
		return m
	}
	if m.rosterPhase != rosterReady || m.roster == nil {
		m = m.refreshRoster()
	}
	if m.roster == nil || m.rosterPhase != rosterReady {
		m.notice = "ростер не прочитан: " + m.rosterErr
		return m
	}
	m.applyPlan = plan.ID
	lines := []string{fmt.Sprintf("%s %s [%s]", plan.ID, plan.Title, plan.Status)}
	if m.roster.Preset == "" {
		lines = append(lines, "вариант: нет активного, шапку дают условия в силе")
	} else {
		lines = append(lines, "вариант: "+m.roster.Preset)
	}
	report, err := m.roster.ExplainPlan(plan.ID)
	if err != nil {
		m.applyPlan = ""
		return m.openDialog(dialogApply, "Шапка "+plan.ID+" из ростера",
			append(lines, refusalLines(err.Error())...),
			[]string{"Esc — закрыть"})
	}
	if len(report.Changes) == 0 {
		lines = append(lines, "", "изменений нет: шапка уже совпадает с ростером")
	} else {
		for _, change := range report.Changes {
			lines = append(lines, applyChangeLine(change))
		}
	}
	if len(report.Warnings) > 0 {
		lines = append(lines, "")
		for _, warning := range report.Warnings {
			lines = append(lines, "⚠ "+warning)
		}
	}
	m = m.openDialog(dialogApply, "Шапка "+plan.ID+" из ростера", lines,
		[]string{"Enter — записать шапку · Esc — отмена"})
	// The lock is re-read at the dialog, not at load: a runner can appear
	// while the workspace is open, and the reason belongs on this screen.
	if m.runIsActive() || (m.controller != nil && !m.controllerDone) {
		return m.applyRefusal("идёт прогон: шапка пишется, когда прогон не держит run.lock")
	}
	m.refreshLock()
	if notice, refused := m.lockRefusal("the roster apply is refused"); refused {
		return m.applyRefusal(notice)
	}
	return m
}

// applyChangeLine renders one header key the roster rewrites as the CLI
// prints it: "key: old → new", a missing side as a dash.
func applyChangeLine(change roster.ExplainChange) string {
	old, next := change.Old, change.New
	if old == "" {
		old = "-"
	}
	if next == "" {
		next = "-"
	}
	return fmt.Sprintf("%s: %s → %s", change.Key, old, next)
}

// refusalLines marks a refusal inside the dialog body: the first line carries
// the mark, the rest of the message keeps its own lines.
func refusalLines(reason string) []string {
	lines := strings.Split(strings.TrimSpace(reason), "\n")
	out := make([]string, 0, len(lines)+1)
	out = append(out, "")
	if len(lines) == 0 || lines[0] == "" {
		return append(out, "✗ the roster apply was refused")
	}
	out = append(out, "✗ "+lines[0])
	for _, line := range lines[1:] {
		out = append(out, "  "+line)
	}
	return out
}

// applyRefusal appends a refusal to the open dialog and keeps it open: the
// file is written by the same roster.Apply as the CLI, so a refusal leaves
// it as it was, and the person can read the reason next to the diff.
func (m Model) applyRefusal(reason string) Model {
	m.dialogLines = append(m.dialogLines, refusalLines(reason)...)
	return m
}

// applyPlanHeader writes the header of the remembered plan through the
// roster workspace. The guards run in the order apply needs them: a live
// run of this workspace first, then the checkout lock of another runner,
// then roster.Apply itself, which refuses an open unit without a chain and
// restores the plan when lint rejects the written header.
func (m Model) applyPlanHeader() (Model, tea.Cmd) {
	planID := m.applyPlan
	if planID == "" || m.roster == nil {
		m.dialog = dialogNone
		return m, nil
	}
	if m.runIsActive() || (m.controller != nil && !m.controllerDone) {
		return m.applyRefusal("идёт прогон: шапка пишется, когда прогон остановлен"), nil
	}
	m.refreshLock()
	if notice, refused := m.lockRefusal("the roster apply is refused"); refused {
		return m.applyRefusal(notice), nil
	}
	result, _, err := m.roster.ApplyPlan(planID, false)
	if err != nil {
		return m.applyRefusal(err.Error()), nil
	}
	if result.Code != clierrors.EXIT_OK {
		reason := strings.TrimSpace(result.Stderr)
		if reason == "" {
			reason = strings.TrimSpace(result.Stdout)
		}
		if reason == "" {
			reason = fmt.Sprintf("roster apply exited %d", result.Code)
		}
		if result.Code == clierrors.EXIT_NOTHING {
			return m.applyRefusal("изменений нет: шапка уже совпадает с ростером"), nil
		}
		return m.applyRefusal(reason), nil
	}
	// The written header changes the card's drift and the tree: the roster
	// is reloaded in place and the plans through the load command.
	m.dialog = dialogNone
	m.applyPlan = ""
	m = m.refreshRoster()
	m.notice = applyNotice(result.Stdout, planID)
	return m, loadCmd(m.load)
}

// applyNotice is the outcome line of a written header: the commit reminder
// apply prints, or that reminder built from the plan when apply printed no
// such line.
func applyNotice(stdout, planID string) string {
	for _, line := range strings.Split(stdout, "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "commit: ") {
			return trimmed
		}
	}
	return fmt.Sprintf("commit: %s: apply roster", planID)
}
