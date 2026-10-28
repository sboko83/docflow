package cycle

import (
	"fmt"
	"path/filepath"
	"sort"
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
	"   Handoff `Unblocked:` after the latest `Blocked (human)` — a person did the\n" +
	"   step; a legacy `Blocked:` item counts as human. Before `Do:`\n" +
	"   check that condition with a command first (a file, a login, a device\n" +
	"   state). Met → go on with `Do:`; not met → run\n" +
	"   `docflow block {id} --needs human --reason \"<what is still missing>\"` and\n" +
	"   stop, without doing the rest of `Do:`.\n" +
	"2. Optionally run `docflow verify {id}` for intermediate checks.\n" +
	"3. Run `docflow finish {id} --handoff \"<what the next unit must know and the\n" +
	"   code does not show>\"`; repeat `--handoff` per item. A decision made on the\n" +
	"   way: `--decision \"<decision — why — rejected alternative>\"`. It runs\n" +
	"   verify, done and commit; on a failure fix it and run `finish` again.\n" +
	"Do not edit the plan file by hand: docflow changes it.\n"

var promptSections = []string{"Goal", "Constraints", "Prerequisites", "Traps", "Context", "Decisions"}

// PromptText renders the plan sections, unit, dependency handoff, archive
// gotchas, phase diff for review units, and cycle instructions used by
// docflow prompt and lint budgeting.
func PromptText(root string, plan *format.Plan, unit *format.Unit, repos []string) (string, error) {
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
		out.WriteString("`Unverified:` items below stay open on their own units; do not repeat them in your `unverified`.\n\n")
		out.WriteString(strings.Join(handoffs, "\n"))
		out.WriteByte('\n')
	}
	if gotchas := format.ArchiveGotchas(root, plan, unit); len(gotchas) > 0 {
		out.WriteString("## Archive gotchas\n\n")
		out.WriteString(strings.Join(gotchas, "\n"))
		out.WriteString("\n\n")
	}
	if reviewUnit(unit) {
		section, err := phaseDiffSection(root, plan, unit, repos)
		if err != nil {
			return "", err
		}
		out.WriteString(section)
	}
	if prefix := testChangesPrefix(plan, unit); prefix != "" {
		section, err := testChangesSection(root, plan, unit, repos, prefix)
		if err != nil {
			return "", err
		}
		out.WriteString(section)
	}
	out.WriteString(strings.ReplaceAll(cycleText, "{id}", unit.ID))
	return out.String(), nil
}

func reviewUnit(unit *format.Unit) bool {
	role := unit.Field("Role")
	return role != nil && role.Value == "review"
}

func acceptanceUnit(unit *format.Unit) bool {
	role := unit.Field("Role")
	return role != nil && role.Value == "acceptance"
}

// testChangesPrefix is the commit subject prefix whose commits feed the
// `## Test changes` section: the unit's phase for review, the whole plan for
// acceptance, "" when the unit is neither.
func testChangesPrefix(plan *format.Plan, unit *format.Unit) string {
	switch {
	case reviewUnit(unit) && unit.Phase != nil:
		return fmt.Sprintf("%s.%d.", plan.Meta["id"], *unit.Phase)
	case acceptanceUnit(unit):
		return fmt.Sprintf("%s.", plan.Meta["id"])
	}
	return ""
}

type promptRepository struct {
	path string
	root string
}

// promptRepositories lists the root and the nested repositories of repos:, in
// the order of the prompt's per-repository blocks.
func promptRepositories(root string, repos []string) []promptRepository {
	list := []promptRepository{{path: ".", root: root}}
	for _, repo := range repos {
		if repo == "" || repo == "." {
			continue
		}
		list = append(list, promptRepository{
			path: filepath.ToSlash(repo),
			root: filepath.Join(root, filepath.FromSlash(repo)),
		})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].path == list[j].path {
			return false
		}
		if list[i].path == "." {
			return true
		}
		if list[j].path == "." {
			return false
		}
		return list[i].path < list[j].path
	})
	return list
}

// perRepositorySection writes the section heading and one block per
// repository of repos:; the blocks name their repository by path only when
// there are nested repositories. label names the section in an error.
func perRepositorySection(root string, repos []string, heading, label string, block func(repo string) (string, error)) (string, error) {
	repositories := promptRepositories(root, repos)
	var out strings.Builder
	fmt.Fprintf(&out, "## %s\n\n", heading)
	for i, repo := range repositories {
		if len(repos) > 0 {
			if i > 0 {
				out.WriteByte('\n')
			}
			fmt.Fprintf(&out, "### %s\n\n", repo.path)
		}
		text, err := block(repo.root)
		if err != nil {
			return "", fmt.Errorf("%s in %s: %w", label, repo.path, err)
		}
		out.WriteString(text)
		out.WriteByte('\n')
	}
	return out.String(), nil
}

func phaseDiffSection(root string, plan *format.Plan, unit *format.Unit, repos []string) (string, error) {
	if unit.Phase == nil {
		return "", fmt.Errorf("review unit %s has no phase", unit.ID)
	}
	prefix := fmt.Sprintf("%s.%d.", plan.Meta["id"], *unit.Phase)
	return perRepositorySection(root, repos, "Phase diff", "phase diff", func(repo string) (string, error) {
		return phaseDiffForRepository(repo, prefix, unit.ID)
	})
}

// testChangesSection lists the `Test changes:` items of the unit commits in
// the range of prefix: the phase for review, the whole plan for acceptance.
func testChangesSection(root string, plan *format.Plan, unit *format.Unit, repos []string, prefix string) (string, error) {
	return perRepositorySection(root, repos, "Test changes", "test changes", func(repo string) (string, error) {
		return testChangesForRepository(repo, prefix, unit.ID)
	})
}

// phaseBase returns the parent of the first commit whose subject carries the
// prefix, the unit's own commits skipped. ok is false when no commit does.
func phaseBase(root, prefix, unitID string) (base string, ok bool, err error) {
	log, err := gitx.Git(root, "log", "--reverse", "--format=%H%x00%P%x00%s", "HEAD")
	if err != nil {
		return "", false, err
	}
	for _, line := range strings.Split(strings.TrimRight(string(log), "\r\n"), "\n") {
		fields := strings.SplitN(line, "\x00", 3)
		if len(fields) != 3 || !strings.HasPrefix(fields[2], prefix) || reviewUnitCommit(fields[2], unitID) {
			continue
		}
		parents := strings.Fields(fields[1])
		if len(parents) == 0 {
			return "", false, fmt.Errorf("phase commit %s has no parent", fields[0])
		}
		return parents[0], true, nil
	}
	return "", false, nil
}

func phaseDiffForRepository(root, prefix, reviewUnitID string) (string, error) {
	base, ok, err := phaseBase(root, prefix, reviewUnitID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "no commits with prefix " + prefix + "\n", nil
	}
	baseShort, err := gitx.Git(root, "rev-parse", "--short", base)
	if err != nil {
		return "", err
	}
	baseShortText := strings.TrimSpace(string(baseShort))
	phaseLog, err := gitx.Git(root, "log", "--reverse", "--format=%h %s", base+"..HEAD")
	if err != nil {
		return "", err
	}
	commits := make([]string, 0)
	for _, line := range strings.Split(strings.TrimRight(string(phaseLog), "\r\n"), "\n") {
		fields := strings.SplitN(line, " ", 2)
		if len(fields) == 2 && strings.HasPrefix(fields[1], prefix) && !reviewUnitCommit(fields[1], reviewUnitID) {
			commits = append(commits, line)
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Base: %s\n\nCommits:\n%s\nCommand: `git diff %s HEAD`\n", baseShortText, strings.Join(commits, "\n"), baseShortText)
	return out.String(), nil
}

// testChangesForRepository names every test change item of the commits in the
// range base..HEAD as `- <subject>: <item>`, the same range and the same
// commit-skipping as phaseDiffForRepository. The items come from the
// `Test changes:` block each unit commit already carries: git stays the
// source of truth for review and acceptance. `none` when the range has none.
func testChangesForRepository(root, prefix, unitID string) (string, error) {
	base, ok, err := phaseBase(root, prefix, unitID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "none\n", nil
	}
	log, err := gitx.Git(root, "log", "--reverse", "--format=%s%x00%b%x00", base+"..HEAD")
	if err != nil {
		return "", err
	}
	tokens := strings.Split(string(log), "\x00")
	found := false
	var out strings.Builder
	for i := 0; i+1 < len(tokens); i += 2 {
		subject := strings.TrimSpace(tokens[i])
		if !strings.HasPrefix(subject, prefix) || reviewUnitCommit(subject, unitID) {
			continue
		}
		body := strings.TrimRight(tokens[i+1], "\r\n")
		for _, item := range commitTestChanges(body) {
			fmt.Fprintf(&out, "- %s: %s\n", subject, item)
			found = true
		}
	}
	if !found {
		return "none\n", nil
	}
	return out.String(), nil
}

func reviewUnitCommit(subject, unitID string) bool {
	if subject == unitID {
		return true
	}
	if !strings.HasPrefix(subject, unitID) || len(subject) == len(unitID) {
		return false
	}
	next := subject[len(unitID)]
	return next == ':' || unicode.IsSpace(rune(next))
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
		needs := blockNeeds(selection.Unit)
		suffix := ""
		if needs != "" {
			suffix = " (needs " + needs + ")"
		}
		lines = append(lines, fmt.Sprintf("blocked: %s %s%s", selection.Unit.ID, selection.Unit.Title, suffix))
		if needs == "human" {
			lines = append(lines, fmt.Sprintf("unblock: docflow unblock %s --note \"…\"", selection.Unit.ID))
		}
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
	var repos []string
	if unit.Status == " " || reviewUnit(unit) || acceptanceUnit(unit) {
		if projectRepos != nil {
			repos, err = projectRepos(root)
			if err != nil {
				return Result{}, err
			}
		}
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
	text, err := PromptText(root, plan, unit, repos)
	if err != nil {
		return Result{}, err
	}
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
