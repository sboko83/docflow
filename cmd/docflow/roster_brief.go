package main

import (
	"fmt"
	"strings"
	"time"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/roster"
)

// planHeaderKeys is the plan-header order `roster explain` compares: the
// same keys `roster apply` writes, so an explain line is a change apply
// would make.
var planHeaderKeys = []string{"executor", "fallback", "cheap", "heavy", "review", "acceptance", "replan"}

// ExplainChange is one header key where the plan and the brief disagree:
// the value in the plan and the value the brief gives now. An empty Old
// means the key is absent, an empty New means apply would remove it.
type ExplainChange struct {
	Key string `json:"key"`
	Old string `json:"old"`
	New string `json:"new"`
}

// ExplainReport is the JSON of `roster explain`.
type ExplainReport struct {
	Plan     string          `json:"plan"`
	Path     string          `json:"path"`
	Changes  []ExplainChange `json:"changes"`
	Warnings []string        `json:"warnings"`
}

// rosterApply resolves the policy with the conditions of the call and
// writes the brief header into the plan. apply reads the version 2 policy
// itself, so a v1 file fails here with the migration hint.
func rosterApply(parsed *parsedArgs, root, planID string) error {
	policy, state, err := rosterConditionPolicy(parsed)
	if err != nil {
		return err
	}
	catalog, err := roster.LoadCatalog("")
	if err != nil {
		return err
	}
	result, report, err := roster.Apply(root, planID, policy, state, catalog, rosterFlag(parsed, "--dry-run"))
	if err != nil {
		return err
	}
	if rosterFlag(parsed, "--json") {
		stderr := ""
		switch result.Code {
		case clierrors.EXIT_OK, clierrors.EXIT_NOTHING:
			// The report carries the warnings; a success stays quiet.
		default:
			stderr = result.Stderr
		}
		return rosterJSONResult(report, stderr, result.Code)
	}
	return commandResult{code: result.Code, stdout: result.Stdout, stderr: result.Stderr}
}

// rosterBriefCommand prints the brief of the roster: the conditions in
// force, the chain and unit budget of every role, the review matrix and
// the warnings. The condition flags of the call are merged in memory and
// never written: a flag is a question, not a condition.
func rosterBriefCommand(parsed *parsedArgs) error {
	policy, state, err := rosterConditionPolicy(parsed)
	if err != nil {
		return err
	}
	catalog, err := roster.LoadCatalog("")
	if err != nil {
		return err
	}
	brief, err := roster.BuildBrief(policy, state, catalog, nil)
	if err != nil {
		return err
	}
	if rosterFlag(parsed, "--json") {
		return rosterJSON(brief, "")
	}
	return commandResult{code: clierrors.EXIT_OK, stdout: brief.Text()}
}

// rosterExplain compares the header of a plan with the brief of the roster
// and prints the keys apply would rewrite. It only reads: the plan and the
// state file stay as they are.
func rosterExplain(parsed *parsedArgs) error {
	planID, _ := parsed.values["id"].(string)
	if planID == "" {
		return clierrors.DocflowError{Message: "docflow: roster explain: PLAN is required", Code: clierrors.EXIT_FORMAT}
	}
	root, err := projectRoot()
	if err != nil {
		return err
	}
	plan, err := cycle.FindPlan(root, planID)
	if err != nil {
		return err
	}
	policy, state, err := rosterConditionPolicy(parsed)
	if err != nil {
		return err
	}
	catalog, err := roster.LoadCatalog("")
	if err != nil {
		return err
	}
	brief, err := roster.BuildBrief(policy, state, catalog, planRoles(plan))
	if err != nil {
		return err
	}
	report := ExplainReport{
		Plan:     plan.Meta["id"],
		Path:     plan.Path,
		Changes:  headerChanges(plan, brief.Header()),
		Warnings: append([]string{}, brief.Warnings...),
	}
	if rosterFlag(parsed, "--json") {
		return rosterJSON(report, "")
	}
	return commandResult{code: clierrors.EXIT_OK, stdout: explainText(report)}
}

// rosterConditionPolicy loads the v2 policy and its condition state with
// the one-shot flags of the call applied to the in-memory state. The state
// file is never written here: `roster off` and its siblings are the only
// commands that change it.
func rosterConditionPolicy(parsed *parsedArgs) (*roster.Policy, *roster.State, error) {
	path := roster.PolicyPath(rosterString(parsed, "--roster"))
	// A v1 file fails here with the migration hint.
	policy, err := roster.LoadPolicy(path)
	if err != nil {
		return nil, nil, err
	}
	state, err := roster.LoadState(roster.StatePath(path))
	if err != nil {
		return nil, nil, err
	}
	if err := applyBriefFlags(parsed, state); err != nil {
		return nil, nil, err
	}
	// A condition naming an access or preset the policy does not declare
	// fails before the brief is built: a typo must not read as "no effect".
	if err := state.Validate(policy); err != nil {
		return nil, nil, err
	}
	return policy, state, nil
}

// applyBriefFlags merges the one-shot condition flags into the state:
// --preset, the budget levels, --off, --expires and --only. The lists take
// comma-separated accesses, so one flag can name several.
func applyBriefFlags(parsed *parsedArgs, state *roster.State) error {
	if preset := rosterString(parsed, "--preset"); preset != "" {
		state.Preset = preset
	}
	for _, flag := range []string{"--off", "--minimal", "--lean", "--prefer"} {
		level := strings.TrimPrefix(flag, "--")
		for _, value := range briefFlagValues(parsed, flag) {
			for _, access := range splitBriefList(value) {
				if access == "" {
					return briefFlagError(flag, "ACCESS[,ACCESS...]")
				}
				if flag == "--off" {
					// An open-ended off, the same a bare `roster off` writes.
					state.SetOff(access, time.Time{})
					continue
				}
				if err := state.SetBudget(access, level); err != nil {
					return err
				}
			}
		}
	}
	for _, value := range briefFlagValues(parsed, "--expires") {
		for _, entry := range splitBriefList(value) {
			access, day, ok := strings.Cut(entry, "=")
			if !ok || access == "" || day == "" {
				return briefFlagError("--expires", "ACCESS=DATE")
			}
			when, err := roster.ParseDay(day)
			if err != nil {
				return err
			}
			state.SetExpires(access, when)
		}
	}
	for _, value := range briefFlagValues(parsed, "--only") {
		for _, entry := range splitBriefList(value) {
			access, glob, ok := strings.Cut(entry, "=")
			if !ok || access == "" || glob == "" {
				return briefFlagError("--only", "ACCESS=GLOB")
			}
			if err := state.Allow(access, []string{glob}); err != nil {
				return err
			}
		}
	}
	return nil
}

// briefFlagNames lists the one-shot condition flags a call carries, so a
// command can reject them where they make no sense.
func briefFlagNames(parsed *parsedArgs) []string {
	var names []string
	if rosterString(parsed, "--preset") != "" {
		names = append(names, "--preset")
	}
	for _, flag := range []string{"--off", "--minimal", "--lean", "--prefer", "--expires", "--only"} {
		if len(briefFlagValues(parsed, flag)) > 0 {
			names = append(names, flag)
		}
	}
	return names
}

// briefFlagValues returns the values a repeated flag collected.
func briefFlagValues(parsed *parsedArgs, name string) []string {
	values, _ := parsed.values[name].([]string)
	return values
}

// splitBriefList splits a comma-separated flag value and trims the spaces
// around every item.
func splitBriefList(value string) []string {
	items := strings.Split(value, ",")
	for i := range items {
		items[i] = strings.TrimSpace(items[i])
	}
	return items
}

// briefFlagError names the flag and the shape it wants.
func briefFlagError(flag, want string) error {
	return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster: %s wants %s", flag, want), Code: clierrors.EXIT_FORMAT}
}

// planRoles names the roles a plan uses: every Role: of a unit, executor
// for units without one and replan for the runner's replanning. The brief
// is cut to these roles, so a warning about a role the plan never runs
// stays out of the explain output.
func planRoles(plan *format.Plan) []string {
	return roster.PlanRoles(plan)
}

// headerChanges compares the header of the plan with the brief: one entry
// per key that differs. A key the plan holds but the brief leaves empty is
// a removal; a key both miss is not a change.
func headerChanges(plan *format.Plan, header map[string]string) []ExplainChange {
	changes := make([]ExplainChange, 0)
	for _, key := range planHeaderKeys {
		wanted, has := header[key]
		old, exists := plan.Meta[key]
		if !has && !exists {
			continue
		}
		if has && exists && old == wanted {
			continue
		}
		changes = append(changes, ExplainChange{Key: key, Old: old, New: wanted})
	}
	return changes
}

// explainText renders the explain report: the plan, one "key: old → new"
// line per change and the warnings of the roles the plan uses.
func explainText(report ExplainReport) string {
	var out strings.Builder
	fmt.Fprintf(&out, "plan: %s\n", report.Plan)
	if len(report.Changes) == 0 {
		out.WriteString("changes: none\n")
	} else {
		out.WriteString("changes:\n")
		for _, change := range report.Changes {
			fmt.Fprintf(&out, "%s: %s → %s\n", change.Key, planValueText(change.Old), planValueText(change.New))
		}
	}
	if len(report.Warnings) == 0 {
		out.WriteString("warnings: none\n")
	} else {
		out.WriteString("warnings:\n")
		for _, warning := range report.Warnings {
			fmt.Fprintf(&out, "- %s\n", warning)
		}
	}
	return out.String()
}

// planValueText prints an absent header value as a dash, the same way a
// plan writes "no route" in a chain.
func planValueText(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
