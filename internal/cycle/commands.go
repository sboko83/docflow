package cycle

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/gitx"
)

const cycleText = "## Cycle\n" +
	"1. Do exactly what `Do:` says, no less and no more. If `Do:` contradicts\n" +
	"   reality or the plan is outdated, run\n" +
	"   `docflow block {id} --reason \"<why>\"` and stop.\n" +
	"2. Optionally run `docflow verify {id}` for intermediate checks.\n" +
	"3. Run `docflow finish {id} --handoff \"<what the next unit must know and the\n" +
	"   code does not show>\"`; repeat `--handoff` per item. A decision made on the\n" +
	"   way: `--decision \"<decision — why — rejected alternative>\"`. It runs\n" +
	"   verify, done and commit; on a failure fix it and run `finish` again.\n" +
	"Do not edit the plan file by hand: docflow changes it.\n"

var promptSections = []string{"Goal", "Constraints", "Prerequisites", "Traps", "Context", "Decisions"}

// PromptText renders the plan sections, unit, dependency handoff, archive
// gotchas, and cycle instructions used by docflow prompt and lint budgeting.
func PromptText(root string, plan *format.Plan, unit *format.Unit) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# %s %s\n\nPlan: %s %s (%s)\n\n",
		unit.ID, unit.Title, plan.Meta["id"], plan.Meta["title"], plan.Path)
	for _, name := range promptSections {
		if section := plan.Section(name); section != nil {
			out.WriteString(strings.TrimRightFunc(normalizeNewlines(rawPlanText(plan, section.Line, section.End)), unicode.IsSpace))
			out.WriteString("\n\n")
		}
	}
	out.WriteString("## Unit\n\n")
	out.WriteString(strings.TrimRightFunc(normalizeNewlines(rawPlanText(plan, unit.Line, unit.End)), unicode.IsSpace))
	out.WriteString("\n\n")
	depends, _ := format.UnitDepends(plan, unit)
	handoffs := make([]string, 0)
	for _, id := range depends {
		dep := plan.Unit(id)
		if dep == nil {
			continue
		}
		field := dep.Field("Handoff")
		if field != nil {
			text := normalizeNewlines(rawPlanText(plan, field.Line+1, field.End))
			handoffs = append(handoffs, fmt.Sprintf("### %s\n%s\n", id, strings.TrimRightFunc(text, unicode.IsSpace)))
		}
	}
	if len(handoffs) > 0 {
		out.WriteString("## Handoff from Depends\n\n")
		out.WriteString(strings.Join(handoffs, "\n"))
		out.WriteByte('\n')
	}
	if gotchas := format.ArchiveGotchas(root, plan, unit); len(gotchas) > 0 {
		out.WriteString("## Archive gotchas\n\n")
		out.WriteString(strings.Join(gotchas, "\n"))
		out.WriteString("\n\n")
	}
	out.WriteString(strings.ReplaceAll(cycleText, "{id}", unit.ID))
	return out.String()
}

func rawPlanText(plan *format.Plan, start, end int) string {
	if start < 1 || start > len(plan.Raw) {
		return ""
	}
	if end > len(plan.Raw) {
		end = len(plan.Raw)
	}
	if end < start-1 {
		return ""
	}
	return strings.Join(plan.Raw[start-1:end], "")
}

func normalizeNewlines(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

// Result is captured command output and its CLI exit status.
type Result struct {
	Code   int
	Stdout string
	Stderr string
}

// Next selects a unit and formats the `docflow next` response.
func Next(root string) (Result, error) {
	docflow, err := readDocflow(root)
	if err != nil {
		return Result{}, err
	}
	selection, err := SelectUnit(root, docflow)
	if err != nil {
		return Result{}, err
	}
	switch selection.Kind {
	case "unit":
		return Result{Code: clierrors.EXIT_OK, Stdout: selection.Unit.ID + "\n"}, nil
	case "blocked":
		return Result{Code: clierrors.EXIT_BLOCKED, Stdout: selection.Unit.ID + "\n"}, nil
	default:
		if selection.Plan != nil {
			return Result{Code: clierrors.EXIT_NOTHING, Stderr: fmt.Sprintf("%s: no unit with finished Depends\n", selection.Plan.Meta["id"])}, nil
		}
		return Result{Code: clierrors.EXIT_NOTHING, Stderr: "nothing to do\n"}, nil
	}
}

// Status formats the queue, current selection, and the already-computed lint
// summary line.
func Status(root, lintLine string) (Result, error) {
	docflow, err := readDocflow(root)
	if err != nil {
		return Result{}, err
	}
	lines := make([]string, 0, len(docflow.Queue)+5)
	if len(docflow.Queue) == 0 {
		lines = append(lines, "queue is empty")
	}
	for _, item := range docflow.Queue {
		plan, err := findPlan(root, item.Text)
		if err != nil {
			return Result{}, err
		}
		done := 0
		for _, unit := range plan.Units {
			pending, err := commitPending(root, plan, unit)
			if err != nil {
				return Result{}, err
			}
			if DoneStatuses[unit.Status] && !pending {
				done++
			}
		}
		id := plan.Meta["id"]
		if id == "" {
			id = item.Text
		}
		line := fmt.Sprintf("%s %s [%s] %d/%d", id, plan.Meta["title"], plan.Meta["status"], done, len(plan.Units))
		if len(plan.Units) > 0 && done == len(plan.Units) {
			line += " ready to close"
		}
		lines = append(lines, line)
	}
	if len(docflow.Frozen) > 0 {
		ids := make([]string, 0, len(docflow.Frozen))
		for _, item := range docflow.Frozen {
			words := strings.Fields(item.Text)
			if len(words) > 0 {
				ids = append(ids, words[0])
			}
		}
		lines = append(lines, "frozen: "+strings.Join(ids, ", "))
	}
	selection, err := SelectUnit(root, docflow)
	if err != nil {
		return Result{}, err
	}
	switch selection.Kind {
	case "unit":
		suffix := ""
		pending, err := commitPending(root, selection.Plan, selection.Unit)
		if err != nil {
			return Result{}, err
		}
		if pending {
			suffix = " (commit pending)"
		}
		lines = append(lines, fmt.Sprintf("current: %s %s%s", selection.Unit.ID, selection.Unit.Title, suffix))
	case "blocked":
		lines = append(lines, fmt.Sprintf("blocked: %s %s", selection.Unit.ID, selection.Unit.Title))
	default:
		lines = append(lines, "current: none")
	}
	if value := docflow.Meta["start_read"]; value != "" {
		lines = append(lines, "start: "+value)
	}
	lines = append(lines, lintLine)
	return Result{Code: clierrors.EXIT_OK, Stdout: strings.Join(lines, "\n") + "\n"}, nil
}

func promptUnit(root string, docflow *format.DocflowFile, unitID string) (*format.Plan, *format.Unit, Result, error) {
	if unitID == "" {
		selection, err := SelectUnit(root, docflow)
		if err != nil {
			return nil, nil, Result{}, err
		}
		if selection.Kind == "blocked" {
			return nil, nil, Result{Code: clierrors.EXIT_BLOCKED, Stderr: selection.Unit.ID + " is blocked\n"}, nil
		}
		if selection.Kind == "nothing" {
			return nil, nil, Result{Code: clierrors.EXIT_NOTHING, Stderr: "nothing to do\n"}, nil
		}
		return selection.Plan, selection.Unit, Result{}, nil
	}
	plan, unit, err := findUnit(root, unitID)
	if err != nil {
		return nil, nil, Result{}, err
	}
	if err := checkNotFrozen(plan); err != nil {
		return nil, nil, Result{}, err
	}
	if plan.Meta["status"] == "draft" {
		return nil, nil, Result{}, clierrors.DocflowError{Message: fmt.Sprintf("%s: plan is a draft", plan.Path), Code: clierrors.EXIT_FORMAT}
	}
	if unit.Status == "!" {
		return nil, nil, Result{Code: clierrors.EXIT_BLOCKED, Stderr: unitID + " is blocked\n"}, nil
	}
	if DoneStatuses[unit.Status] {
		return nil, nil, Result{Code: clierrors.EXIT_ORDER, Stderr: fmt.Sprintf("%s is already [%s]\n", unitID, unit.Status)}, nil
	}
	if unit.Status == " " {
		plans, err := queuePlans(root, docflow)
		if err != nil {
			return nil, nil, Result{}, err
		}
		for _, candidate := range plans {
			for _, current := range candidate.Units {
				pending, err := commitPending(root, candidate, current)
				if err != nil {
					return nil, nil, Result{}, err
				}
				if current.Status == "~" || pending {
					return nil, nil, Result{Code: clierrors.EXIT_ORDER, Stderr: current.ID + " is in progress\n"}, nil
				}
			}
		}
		statuses := make(map[string]string, len(plan.Units))
		for _, candidate := range plan.Units {
			statuses[candidate.ID] = candidate.Status
		}
		depends, err := format.UnitDepends(plan, unit)
		if err != nil {
			return nil, nil, Result{}, err
		}
		pending := make([]string, 0)
		for _, dependency := range depends {
			if !DoneStatuses[statuses[dependency]] {
				pending = append(pending, dependency)
			}
		}
		if len(pending) > 0 {
			return nil, nil, Result{Code: clierrors.EXIT_ORDER, Stderr: fmt.Sprintf("%s depends on unfinished %s\n", unitID, strings.Join(pending, ", "))}, nil
		}
	}
	return plan, unit, Result{}, nil
}

// Prompt selects a plan unit, advances a ready unit to doing, and renders its
// prompt while accounting for referenced file bytes.
func Prompt(root, unitID string, projectRepos func(string) ([]string, error)) (Result, error) {
	docflow, err := readDocflow(root)
	if err != nil {
		return Result{}, err
	}
	plan, unit, early, err := promptUnit(root, docflow, unitID)
	if err != nil || early.Code != 0 {
		return early, err
	}
	if unit.Status == "x" {
		return Result{Code: clierrors.EXIT_ORDER, Stderr: fmt.Sprintf("%s: commit pending; run docflow run or docflow finish %s\n", unit.ID, unit.ID)}, nil
	}
	refBytes := 0
	refs := append([]format.Item{}, format.ContextRefs(plan)...)
	if read := unit.Field("Read"); read != nil {
		refs = append(refs, read.Items...)
	}
	for _, item := range refs {
		size, err := format.RefSize(root, item.Text)
		if err != nil {
			return Result{}, clierrors.FormatError{Path: plan.Path, Line: item.Line, Text: err.Error()}
		}
		refBytes += size
	}
	resumed := unit.Status == "~"
	if unit.Status == " " {
		var repos []string
		if projectRepos != nil {
			repos, err = projectRepos(root)
			if err != nil {
				return Result{}, err
			}
		}
		dirty, err := gitx.DirtyFiles(root, plan.Path, repos)
		if err != nil {
			return Result{}, err
		}
		if len(dirty) > 0 {
			return Result{Code: clierrors.EXIT_ORDER, Stderr: "working tree is dirty:\n  " + strings.Join(dirty, "\n  ") + "\n"}, nil
		}
		if err := plan.SetStatus(unit.ID, "~"); err != nil {
			return Result{}, err
		}
		if err := plan.Save(filepath.Join(root, filepath.FromSlash(plan.Path))); err != nil {
			return Result{}, err
		}
		unit = plan.Unit(unit.ID)
	}
	text := PromptText(root, plan, unit)
	tokens := (len([]byte(text)) + refBytes) / 4
	record := readStats(root, unit.ID)
	if _, ok := record["proxy_tokens"]; !ok {
		stamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
		data := map[string]any{"unit": unit.ID, "proxy_tokens": tokens, "prompted_at": stamp}
		for key, value := range record {
			data[key] = value
		}
		if err := writeStats(root, unit.ID, data); err != nil {
			return Result{}, err
		}
	}
	budget := "None"
	if value, err := strconv.Atoi(docflow.Meta["read_budget"]); err == nil && value > 0 {
		budget = strconv.Itoa(value)
	}
	mark := ""
	if resumed {
		mark = " (resumed)"
	}
	stderr := fmt.Sprintf("%s%s: ~%d tokens of context (prompt + Context + Read), read_budget %s\n", unit.ID, mark, tokens, budget)
	return Result{Code: clierrors.EXIT_OK, Stdout: text, Stderr: stderr}, nil
}
