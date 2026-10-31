package roster

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/lint"
)

// HeaderChange is one header key roster apply v2 rewrites: the value in
// the plan and the value the roster gives now. An empty Old means the key
// is absent, an empty New means apply would remove it.
type HeaderChange struct {
	Key string `json:"key"`
	Old string `json:"old"`
	New string `json:"new"`
}

// ApplyReport is the machine-readable result of roster apply v2: the plan,
// the keys it rewrites, the warnings of the roster and of the units, and
// whether the call was a dry run. It carries no plan text and no file
// content, so --json never leaks a secret from the roster.
type ApplyReport struct {
	Plan     string         `json:"plan"`
	Path     string         `json:"path"`
	DryRun   bool           `json:"dry_run,omitempty"`
	Changes  []HeaderChange `json:"changes"`
	Warnings []string       `json:"warnings"`
}

// PlanRoles names the roles a plan uses: executor for every unit without a
// Role: and for the plan itself, replan for the runner's replanning, and
// every Role: a unit names. The brief is cut to these roles, so a warning
// about a role the plan never runs stays out of apply.
func PlanRoles(plan *format.Plan) []string {
	used := []string{Roles[0], "replan"}
	seen := map[string]bool{Roles[0]: true, "replan": true}
	for _, unit := range plan.Units {
		field := unit.Field("Role")
		if field == nil || seen[field.Value] {
			continue
		}
		seen[field.Value] = true
		used = append(used, field.Value)
	}
	return used
}

// ApplyV2 writes the chains the v2 roster resolves into the plan header:
// executor: and fallback: from the executor chain, replan:, and every role
// key a unit Role: references; role keys without units, a fallback without
// a tail and an executor without a route are removed, every other
// frontmatter key keeps its place and order. The header comes from
// Brief.Header(), so the routes are rendered by the same rule the brief
// prints. A dry run reports the changes without touching the file. The
// written plan is linted and restored on lint errors, a run in progress
// stops apply, and apply never commits.
func ApplyV2(root, planID string, policy *Policy, state *State, catalog *Catalog, dryRun bool) (Result, ApplyReport, error) {
	report := ApplyReport{Changes: make([]HeaderChange, 0), Warnings: make([]string, 0)}
	plan, err := cycle.FindPlan(root, planID)
	if err != nil {
		return Result{}, report, err
	}
	report.Plan = plan.Meta["id"]
	report.Path = plan.Path
	report.DryRun = dryRun
	status := plan.Meta["status"]
	switch status {
	case "draft", "active", "frozen":
	default:
		if status == "" {
			status = "without status"
		}
		return Result{}, report, clierrors.DocflowError{
			Message: fmt.Sprintf("docflow: %s: plan is %s, roster apply wants draft, active or frozen", plan.Path, status),
			Code:    clierrors.EXIT_FORMAT,
		}
	}
	if err := checkRunLock(root); err != nil {
		return Result{}, report, err
	}
	if policy == nil {
		return Result{}, report, clierrors.DocflowError{Message: "docflow: roster: no policy to apply", Code: clierrors.EXIT_FORMAT}
	}
	brief, err := BuildBrief(policy, state, catalog, PlanRoles(plan))
	if err != nil {
		return Result{}, report, err
	}
	merged, err := Merge(policy, state)
	if err != nil {
		return Result{}, report, err
	}
	header := brief.Header()
	values := make(map[string]string, len(headerKeys))
	for _, key := range headerKeys {
		values[key] = header[key]
	}
	report.Warnings = append(report.Warnings, brief.Warnings...)
	report.Warnings = append(report.Warnings, unitWarnings(root, plan, brief, merged)...)
	for _, key := range headerKeys {
		value, wanted := values[key], values[key] != ""
		old, exists := plan.Meta[key]
		if !wanted && !exists {
			continue
		}
		if wanted && exists && old == value {
			continue
		}
		report.Changes = append(report.Changes, HeaderChange{Key: key, Old: old, New: value})
	}
	if len(report.Changes) == 0 {
		return Result{
			Code:   clierrors.EXIT_NOTHING,
			Stdout: fmt.Sprintf("roster: %s: header already matches the roster\n", plan.Meta["id"]),
			Stderr: rosterWarningLines(report.Warnings),
		}, report, nil
	}
	if dryRun {
		return Result{
			Code:   clierrors.EXIT_OK,
			Stdout: headerChangesText(report.Changes) + fmt.Sprintf("dry-run: %s: plan not written\n", plan.Meta["id"]),
			Stderr: rosterWarningLines(report.Warnings),
		}, report, nil
	}
	before := plan.Text()
	if err := writePlanHeader(plan, values); err != nil {
		return Result{}, report, err
	}
	path := filepath.Join(root, filepath.FromSlash(plan.Path))
	if err := plan.Save(path); err != nil {
		return Result{}, report, err
	}
	issues, hasErrors, err := lint.ActivationIssues(root, plan)
	if err != nil || hasErrors {
		if restoreErr := os.WriteFile(path, []byte(before), 0o666); restoreErr != nil && err == nil {
			err = restoreErr
		}
		if err != nil {
			return Result{}, report, err
		}
		stderr := fmt.Sprintf("docflow: %s: roster apply wrote an invalid plan; the plan is restored\n", plan.Path)
		stderr += rosterWarningLines(report.Warnings)
		return Result{Code: clierrors.EXIT_FORMAT, Stderr: stderr + strings.Join(issues, "\n") + "\n"}, report, nil
	}
	var stdout strings.Builder
	stdout.WriteString(headerChangesText(report.Changes))
	fmt.Fprintf(&stdout, "commit: %s: apply roster\n", plan.Meta["id"])
	var stderr strings.Builder
	stderr.WriteString(rosterWarningLines(report.Warnings))
	for _, issue := range issues {
		stderr.WriteString(issue)
		stderr.WriteByte('\n')
	}
	return Result{Code: clierrors.EXIT_OK, Stdout: stdout.String(), Stderr: stderr.String()}, report, nil
}

// unitWarnings reports the units the current conditions do not cover: an
// explicit Executor: on an access the conditions switch off, narrow or
// serve only for another role; an open unit whose Read: proxy is larger
// than the read budget of its role; and, through the brief, a role
// without a fallback. They are advice for the planner, so the caller
// prints them on stderr and keeps the exit code.
func unitWarnings(root string, plan *format.Plan, brief *Brief, merged *Policy) []string {
	budgets := make(map[string]*UnitBudget, len(brief.Roles))
	for _, role := range brief.Roles {
		budgets[role.Role] = role.Budget
	}
	warnings := make([]string, 0)
	for _, unit := range plan.Units {
		role := unitRole(unit)
		if field := unit.Field("Executor"); field != nil {
			if reason := executorCondition(merged, field.Value, role); reason != "" {
				warnings = append(warnings, fmt.Sprintf("unit %s: Executor: %s is outside the current conditions: %s", unit.ID, field.Value, reason))
			}
		}
		if unit.Status == "x" || unit.Status == "-" {
			continue
		}
		budget := budgets[role]
		if budget == nil || budget.Target <= 0 {
			continue
		}
		if proxy := readProxy(root, unit); proxy > budget.Target {
			warnings = append(warnings, fmt.Sprintf("unit %s: Read: proxy ~%d tokens > %s budget %d", unit.ID, proxy, role, budget.Target))
		}
	}
	return warnings
}

// unitRole is the role a unit runs as: its Role: field, or executor when
// it carries none.
func unitRole(unit *format.Unit) string {
	if field := unit.Field("Role"); field != nil {
		return field.Value
	}
	return Roles[0]
}

// executorCondition says why an explicit Executor: route no longer fits
// the conditions in force, or returns "" when it still does. A malformed
// triple is left to lint, which reports it against its own field.
func executorCondition(merged *Policy, value, role string) string {
	agent, model, _, err := lint.ParseExecutor(value)
	if err != nil {
		return ""
	}
	name, id := routeAccess(agent, model)
	access, ok := merged.Access(name)
	if !ok {
		return fmt.Sprintf("access %s is not in the roster", name)
	}
	switch {
	case !contains(access.Agents, agent):
		return fmt.Sprintf("access %s is not reached by %s", name, agent)
	case access.Budget == BudgetOff:
		return fmt.Sprintf("access %s is off", name)
	case !servesBudget(access.Budget, role):
		return fmt.Sprintf("access %s is at budget %s, which serves no %s role", name, access.Budget, role)
	case !access.Expires.IsZero() && time.Now().UTC().After(access.Expires):
		return fmt.Sprintf("access %s expired on %s", name, dayText(access.Expires))
	case !modelAllowed(access, id):
		return fmt.Sprintf("access %s does not allow model %s", name, id)
	}
	return ""
}

// readProxy estimates the tokens a unit reads from its Read: list, the
// same bytes-per-token ratio the runner records as the unit's proxy. A
// reference that cannot be read is skipped: lint reports it against the
// reference, and the budget is advice, not a gate.
func readProxy(root string, unit *format.Unit) int {
	read := unit.Field("Read")
	if read == nil {
		return 0
	}
	size := 0
	for _, item := range read.Items {
		if value, err := format.RefSize(root, item.Text); err == nil {
			size += value
		}
	}
	return size / 4
}

// headerChangesText renders the changes of apply as "key: old → new"; a
// missing side prints as "-", the same way explain prints it.
func headerChangesText(changes []HeaderChange) string {
	var out strings.Builder
	for _, change := range changes {
		fmt.Fprintf(&out, "%s: %s → %s\n", change.Key, headerValueText(change.Old), headerValueText(change.New))
	}
	return out.String()
}

// headerValueText prints an absent header value as a dash.
func headerValueText(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

// rosterWarningLines renders roster warnings as stderr lines; the
// warnings are texts the roster already wrote, this only prefixes them.
func rosterWarningLines(warnings []string) string {
	var out strings.Builder
	for _, warning := range warnings {
		out.WriteString("docflow: roster: " + warning + "\n")
	}
	return out.String()
}
