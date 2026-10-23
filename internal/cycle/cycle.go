// Package cycle selects plan units and implements the unit lifecycle commands.
package cycle

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/gitx"
)

var DoneStatuses = map[string]bool{"x": true, "-": true}

// Selection is the result of choosing a current unit.
type Selection struct {
	Kind string // unit, blocked, or nothing
	Plan *format.Plan
	Unit *format.Unit
}

func readDocflow(root string) (*format.DocflowFile, error) {
	path := filepath.Join(root, filepath.FromSlash(format.DocflowFilePath))
	text, err := format.ReadText(path)
	if err != nil {
		return nil, err
	}
	return format.ParseDocflow(text, format.DocflowFilePath)
}

func findPlan(root, planID string) (*format.Plan, error) {
	plans := filepath.Join(root, "docs", "plans")
	found, err := filepath.Glob(filepath.Join(plans, planID+"-*.md"))
	if err != nil {
		return nil, err
	}
	plain, err := filepath.Glob(filepath.Join(plans, planID+".md"))
	if err != nil {
		return nil, err
	}
	found = append(found, plain...)
	sort.Strings(found)
	if len(found) == 0 {
		return nil, clierrors.DocflowError{Message: fmt.Sprintf("%s: no plan in docs/plans/", planID), Code: clierrors.EXIT_FORMAT}
	}
	rel, err := filepath.Rel(root, found[0])
	if err != nil {
		return nil, err
	}
	text, err := format.ReadText(found[0])
	if err != nil {
		return nil, err
	}
	return format.ParsePlan(text, filepath.ToSlash(rel))
}

func queuePlans(root string, docflow *format.DocflowFile) ([]*format.Plan, error) {
	plans := make([]*format.Plan, 0, len(docflow.Queue))
	for _, item := range docflow.Queue {
		plan, err := findPlan(root, item.Text)
		if err != nil {
			return nil, err
		}
		if plan.Meta["status"] != "draft" {
			plans = append(plans, plan)
		}
	}
	return plans, nil
}

// SelectUnit chooses an in-progress unit first, then a ready unit from the
// first queued plan with work remaining.
func SelectUnit(root string, docflow *format.DocflowFile) (Selection, error) {
	plans, err := queuePlans(root, docflow)
	if err != nil {
		return Selection{}, err
	}
	for _, plan := range plans {
		for _, unit := range plan.Units {
			pending, err := commitPending(root, plan, unit)
			if err != nil {
				return Selection{}, err
			}
			if pending {
				return Selection{Kind: "unit", Plan: plan, Unit: unit}, nil
			}
		}
	}
	for _, plan := range plans {
		for _, unit := range plan.Units {
			if unit.Status == "~" {
				return Selection{Kind: "unit", Plan: plan, Unit: unit}, nil
			}
		}
	}
	for _, plan := range plans {
		for _, unit := range plan.Units {
			if unit.Status == " " || unit.Status == "!" {
				return planSelection(plan)
			}
		}
	}
	return Selection{Kind: "nothing"}, nil
}

func planSelection(plan *format.Plan) (Selection, error) {
	for _, unit := range plan.Units {
		if unit.Status == "~" {
			return Selection{Kind: "unit", Plan: plan, Unit: unit}, nil
		}
	}
	for _, unit := range plan.Units {
		if unit.Status == "!" {
			return Selection{Kind: "blocked", Plan: plan, Unit: unit}, nil
		}
	}
	statuses := make(map[string]string, len(plan.Units))
	for _, unit := range plan.Units {
		statuses[unit.ID] = unit.Status
	}
	for _, unit := range plan.Units {
		if unit.Status != " " {
			continue
		}
		depends, err := format.UnitDepends(plan, unit)
		if err != nil {
			return Selection{}, err
		}
		ready := true
		for _, id := range depends {
			if !DoneStatuses[statuses[id]] {
				ready = false
				break
			}
		}
		if ready {
			return Selection{Kind: "unit", Plan: plan, Unit: unit}, nil
		}
	}
	return Selection{Kind: "nothing", Plan: plan}, nil
}

func findUnit(root, unitID string) (*format.Plan, *format.Unit, error) {
	planID := strings.SplitN(unitID, ".", 2)[0]
	plan, err := findPlan(root, planID)
	if err != nil {
		return nil, nil, err
	}
	unit := plan.Unit(unitID)
	if unit == nil {
		return nil, nil, clierrors.DocflowError{Message: fmt.Sprintf("%s: no unit %s", plan.Path, unitID), Code: clierrors.EXIT_FORMAT}
	}
	return plan, unit, nil
}

func checkNotFrozen(plan *format.Plan) error {
	if plan.Meta["status"] == "frozen" {
		return clierrors.DocflowError{Message: fmt.Sprintf("%s is frozen; run docflow thaw %s", plan.Meta["id"], plan.Meta["id"]), Code: clierrors.EXIT_FORMAT}
	}
	return nil
}

func stateDir(root string) (string, error) {
	output, err := gitx.Git(root, "rev-parse", "--git-dir")
	if err != nil {
		return "", err
	}
	gitDir := strings.TrimSpace(string(output))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	if resolved, err := filepath.EvalSymlinks(gitDir); err == nil {
		gitDir = resolved
	}
	return filepath.Join(gitDir, "docflow", "verified"), nil
}

var execCommand = exec.Command

func commitPending(root string, plan *format.Plan, unit *format.Unit) (bool, error) {
	if unit.Status != "x" {
		return false, nil
	}
	verified, err := stateDir(root)
	if err != nil {
		return false, err
	}
	committing := filepath.Join(filepath.Dir(verified), "committing", unit.ID+".json")
	if info, err := os.Stat(committing); err == nil && info.Mode().IsRegular() {
		return true, nil
	} else if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if info, err := os.Stat(filepath.Join(verified, unit.ID)); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	} else if !info.Mode().IsRegular() {
		return false, nil
	}
	command := execCommand("git", "show", "HEAD:"+plan.Path)
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return true, nil
	}
	previous, err := format.ParsePlan(string(output), plan.Path)
	if err != nil {
		return false, err
	}
	oldUnit := previous.Unit(unit.ID)
	return oldUnit == nil || oldUnit.Status != "x", nil
}
