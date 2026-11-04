package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/gitx"
)

const baselineSkipped = "baseline: skipped, the tree has work in progress"

// baselineGate runs the plan's gate once, before the first unit of a run, when
// the unit is `[ ]` and the tree is clean. A gate red before any change is a
// repository condition, not plan drift: the run stops with code 2 before any
// agent starts instead of spending a unit and a replan on it. A `[~]` unit, a
// pending commit or a dirty tree skips the check, since their work in progress
// is not the plan's starting point. ctx stops the gate on Ctrl-C or a host's
// Pause or Stop; stopped then tells the caller to end the run the usual way.
func baselineGate(ctx context.Context, root string, sel cycle.Selection, p *Progress, totals *runTotals) (code int, stopped bool, err error) {
	gate := strings.TrimSpace(sel.Plan.Meta["gate"])
	if gate == "" || sel.Kind != "unit" || sel.Unit == nil {
		return 0, false, nil
	}
	clean := sel.Unit.Status == " "
	if clean {
		pending, err := cycle.CommitPending(root, sel.Plan, sel.Unit)
		if err != nil {
			return 2, false, err
		}
		clean = !pending
	}
	if clean {
		status, err := gitx.Git(root, "status", "--porcelain")
		if err != nil {
			return 2, false, err
		}
		clean = strings.TrimSpace(string(status)) == ""
	}
	if !clean {
		p.line(baselineSkipped, false, "")
		return 0, false, nil
	}
	p.spin("baseline", nil)
	output, code := cycle.RunPlanCommand(ctx, root, gate)
	p.stop()
	if ctx.Err() != nil {
		return 0, true, nil
	}
	if code == 0 {
		p.line("baseline: gate passed", false, "")
		return 0, false, nil
	}
	logs, err := runDir(root)
	if err != nil {
		return 2, false, err
	}
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return 2, false, err
	}
	// Not `<uid>.*.out`: call-log counting and checkpoint recovery glob those.
	path, err := nextCallLog(logs, "baseline.")
	if err != nil {
		return 2, false, err
	}
	if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
		return 2, false, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return 2, false, err
	}
	totals.stop = fmt.Sprintf("baseline: gate failed (exit %d): %s", code, gate)
	totals.notification = totals.stop
	p.output(cycle.FailureTail(output))
	p.line(totals.stop, true, "")
	p.line("log: "+filepath.ToSlash(rel), true, "")
	return clierrors.EXIT_FORMAT, false, nil
}
