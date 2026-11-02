package runner

import (
	"fmt"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/format"
)

// StartReview is what the interactive start confirmation shows before any
// work begins: the selected unit, the permission mode, and every declared
// prerequisite with its command and whether the run will skip it.
type StartReview struct {
	Plan          string   `json:"plan,omitempty"`
	Title         string   `json:"title,omitempty"`
	Unit          string   `json:"unit,omitempty"`
	Kind          string   `json:"kind"`
	Permissions   string   `json:"permissions,omitempty"`
	Full          bool     `json:"full"`
	Prerequisites []string `json:"prerequisites,omitempty"`
	Assignments   []string `json:"assignments,omitempty"`
	Attempts      int      `json:"attempts,omitempty"`
	Replans       int      `json:"replans,omitempty"`
}

// StartRequirements returns the permission mode and the human-provided
// prerequisites that still need confirmation. It is the single source for the
// terminal prompt and the interactive start dialog.
func StartRequirements(plan *format.Plan) (mode string, items []string) {
	mode = format.PlanPermissions(plan)
	started := format.PlanStarted(plan)
	items = make([]string, 0)
	for _, prerequisite := range format.PlanPrerequisites(plan) {
		if prerequisite.Once && started {
			continue
		}
		if prerequisite.Command == nil {
			items = append(items, prerequisite.Text)
		}
	}
	return mode, items
}

// StartPreview reads the current selection without changing it. A missing or
// broken selection is reported to the caller, which still shows the dialog
// and lets the run fail with its own diagnostic.
func StartPreview(root string, args Args) (StartReview, error) {
	selectionArgs := args
	selectionArgs.RunArgs = agents.RunArgs{}
	sel, err := runSelection(root, selectionArgs)
	if err != nil {
		return StartReview{}, err
	}
	preview := StartReview{Kind: sel.Kind, Attempts: args.Attempts, Replans: args.Replans}
	if sel.Unit != nil {
		preview.Unit = sel.Unit.ID
	}
	if sel.Plan == nil {
		return preview, nil
	}
	preview.Plan = sel.Plan.Meta["id"]
	preview.Title = sel.Plan.Meta["title"]
	preview.Permissions = format.PlanPermissions(sel.Plan)
	preview.Full = preview.Permissions == "full"
	if agentConfig, configErr := agents.RunConfig(sel.Plan, &args.RunArgs); configErr == nil {
		configuration, loadErr := LoadConfiguration(root)
		if loadErr != nil {
			return StartReview{}, loadErr
		}
		// The preview never rewrites the file: it only drops the overrides
		// that no longer apply, so a stale entry cannot refuse the dialog.
		configuration.PruneToPlan(sel.Plan)
		if err := ValidateConfiguration(configuration, sel.Plan, agentConfig); err != nil {
			return StartReview{}, err
		}
		effective, err := EffectiveAssignments(sel.Plan, args.RunArgs, agentConfig, configuration)
		if err != nil {
			return StartReview{}, err
		}
		preview.Assignments = assignmentLines(effective)
	} else {
		return StartReview{}, configErr
	}
	started := format.PlanStarted(sel.Plan)
	for _, prerequisite := range format.PlanPrerequisites(sel.Plan) {
		line := prerequisite.Text
		if prerequisite.Command != nil {
			line += ": `" + *prerequisite.Command + "`"
		}
		switch {
		case prerequisite.Once && started:
			line += " (skipped: once, plan started)"
		case prerequisite.Once:
			line += " (once)"
		}
		if prerequisite.Command == nil && !(prerequisite.Once && started) {
			line += " — needs a person"
		}
		preview.Prerequisites = append(preview.Prerequisites, line)
	}
	if preview.Unit == "" && preview.Kind == "unit" && sel.Unit != nil {
		preview.Unit = sel.Unit.ID
	}
	return preview, nil
}

// Limits names the effective run limits of the interactive confirmation.
func (p StartReview) Limits() string {
	if p.Attempts <= 0 {
		return ""
	}
	return RunLimitsLine(p.Attempts, p.Replans)
}

// RunLimitsLine is the one wording of the controlled run limits shared by the
// start dialog and the live acceptance driver.
func RunLimitsLine(attempts, replans int) string {
	replan := "off"
	if replans > 0 {
		replan = fmt.Sprintf("%d per phase", replans)
	}
	return fmt.Sprintf("attempts %d · auto-replan %s", attempts, replan)
}

func (p StartReview) Summary() string {
	if p.Plan == "" {
		return fmt.Sprintf("nothing to run (%s)", p.Kind)
	}
	title := p.Plan
	if p.Title != "" {
		title += " " + p.Title
	}
	if p.Unit != "" {
		title += " · " + p.Unit
	}
	return title
}
