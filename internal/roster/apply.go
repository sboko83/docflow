package roster

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/lint"
)

// headerKeys is the plan-header order of the keys apply manages: the
// executor pair first, then the role chains and replan.
var headerKeys = []string{"executor", "fallback", "cheap", "heavy", "review", "acceptance", "replan"}

// unitRoles are the header role keys a unit Role: can reference; the
// executor role renders as the executor:/fallback: pair and replan is
// always written.
var unitRoles = Roles[1 : len(Roles)-1]

// anchors are the keys new header keys are inserted after.
var anchors = []string{"executor", "fallback"}

// Result is the outcome of roster apply: the exit code with its output.
type Result struct {
	Code   int
	Stdout string
	Stderr string
}

// Apply picks the chains from the roster and writes them into the plan
// header in docs/plans: executor: and fallback: from the executor chain,
// replan:, and every role key a unit Role: references; role keys without
// units and a fallback without a tail are removed, every other
// frontmatter key keeps its place and order. The written plan is linted
// and restored on lint errors. Apply never commits. The runner does not
// read the roster, so the written plan stays reproducible.
func Apply(root, planID, profileName, rosterPath string) (Result, error) {
	plan, err := cycle.FindPlan(root, planID)
	if err != nil {
		return Result{}, err
	}
	status := plan.Meta["status"]
	switch status {
	case "draft", "active", "frozen":
	default:
		if status == "" {
			status = "without status"
		}
		return Result{}, clierrors.DocflowError{
			Message: fmt.Sprintf("docflow: %s: plan is %s, roster apply wants draft, active or frozen", plan.Path, status),
			Code:    clierrors.EXIT_FORMAT,
		}
	}
	if err := checkRunLock(root); err != nil {
		return Result{}, err
	}
	r, err := Load(rosterPath, profileName)
	if err != nil {
		return Result{}, err
	}
	values := headerValues(r, plan)
	var changes []string
	for _, key := range headerKeys {
		value, wanted := values[key], values[key] != ""
		old, exists := plan.Meta[key]
		if !wanted && !exists {
			continue
		}
		if wanted && exists && old == value {
			continue
		}
		changes = append(changes, changeText(key, old, value, wanted, exists))
	}
	if len(changes) == 0 {
		return Result{Code: clierrors.EXIT_NOTHING, Stdout: fmt.Sprintf("roster: %s: header already matches the roster\n", plan.Meta["id"])}, nil
	}
	before := plan.Text()
	// Update and remove existing keys first; each call reparses, so the
	// meta lines stay fresh. Missing keys follow in reverse order, all
	// after the same anchor, which restores the canonical order.
	for _, key := range headerKeys {
		value := values[key]
		line, exists := plan.MetaLines[key]
		if !exists {
			continue
		}
		if value == "" {
			if err := plan.RemoveLine(line); err != nil {
				return Result{}, err
			}
			continue
		}
		if plan.Meta[key] != value {
			if err := plan.SetMeta(key, value); err != nil {
				return Result{}, err
			}
		}
	}
	anchor := 0
	for _, name := range anchors {
		if line, ok := plan.MetaLines[name]; ok && line > anchor {
			anchor = line
		}
	}
	if anchor == 0 {
		// No executor pair in the header: append at the end of the
		// frontmatter. Resolve the line once, so every insert stacks
		// after the same anchor and the order stays canonical.
		lines := make([]string, len(plan.Raw))
		for i, raw := range plan.Raw {
			lines[i] = strings.TrimRight(raw, "\r\n")
		}
		_, _, body, err := format.ParseMeta(plan.Path, lines)
		if err != nil {
			return Result{}, err
		}
		anchor = body - 1
	}
	for i := len(headerKeys) - 1; i >= 0; i-- {
		key := headerKeys[i]
		value := values[key]
		if value == "" {
			continue
		}
		if _, exists := plan.MetaLines[key]; exists {
			continue
		}
		if err := plan.InsertMeta(anchor, key, value); err != nil {
			return Result{}, err
		}
	}
	path := filepath.Join(root, filepath.FromSlash(plan.Path))
	if err := plan.Save(path); err != nil {
		return Result{}, err
	}
	issues, hasErrors, err := lint.ActivationIssues(root, plan)
	if err != nil || hasErrors {
		if restoreErr := os.WriteFile(path, []byte(before), 0o666); restoreErr != nil && err == nil {
			err = restoreErr
		}
		if err != nil {
			return Result{}, err
		}
		stderr := fmt.Sprintf("docflow: %s: roster apply wrote an invalid plan; the plan is restored\n", plan.Path)
		return Result{Code: clierrors.EXIT_FORMAT, Stderr: stderr + strings.Join(issues, "\n") + "\n"}, nil
	}
	var stdout strings.Builder
	for _, change := range changes {
		stdout.WriteString(change)
		stdout.WriteByte('\n')
	}
	fmt.Fprintf(&stdout, "commit: %s: apply roster\n", plan.Meta["id"])
	var stderr strings.Builder
	for _, issue := range issues {
		stderr.WriteString(issue)
		stderr.WriteByte('\n')
	}
	return Result{Code: clierrors.EXIT_OK, Stdout: stdout.String(), Stderr: stderr.String()}, nil
}

// headerValues maps every managed header key to the chain apply writes;
// an absent value removes an existing key: a role key without units, and
// a fallback without a tail.
func headerValues(r *Roster, plan *format.Plan) map[string]string {
	referenced := make(map[string]bool, len(unitRoles))
	for _, unit := range plan.Units {
		if field := unit.Field("Role"); field != nil {
			referenced[field.Value] = true
		}
	}
	executor := r.Chains["executor"]
	values := map[string]string{
		"executor": agents.RouteText(executor[0]),
		"fallback": chainText(executor[1:]),
		"replan":   chainText(r.Chains["replan"]),
	}
	for _, role := range unitRoles {
		if referenced[role] {
			values[role] = chainText(r.Chains[role])
		}
	}
	return values
}

// changeText renders one header change as "key: old → new"; a missing
// side prints as "-".
func changeText(key, old, value string, wanted, exists bool) string {
	newText := value
	if !wanted {
		newText = "-"
	}
	oldText := old
	if !exists {
		oldText = "-"
	}
	return fmt.Sprintf("%s: %s → %s", key, oldText, newText)
}

// checkRunLock refuses to touch a plan while a run holds its lock: the
// runner would overwrite the header and drop the written chains.
func checkRunLock(root string) error {
	state, err := cycle.StateDir(root)
	if err != nil {
		return err
	}
	path := filepath.Join(filepath.Dir(state), "run", "run.lock")
	if _, err := os.Stat(path); err == nil {
		return clierrors.DocflowError{
			Message: fmt.Sprintf("docflow: %s: a run is in progress; stop it before roster apply", path),
			Code:    clierrors.EXIT_ORDER,
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}
