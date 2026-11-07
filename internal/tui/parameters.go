package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"bsv-docflow/internal/format"
	"bsv-docflow/internal/lint"
)

func (m Model) openParameters() Model {
	plan := m.selectedPlan()
	rows, err := CollectParameters(m.root, plan, m.runLimits)
	if err != nil {
		m.notice = "cannot read parameters: " + err.Error()
		return m
	}
	m.parameters = rows
	m.parameterCursor = min(m.parameterCursor, max(0, len(rows)-1))
	m.parameterEditing = false
	m.dialog = dialogParameters
	m.dialogTitle = "Project parameters"
	m.renderParameters()
	return m
}

func (m *Model) renderParameters() {
	lines := make([]string, 0, len(m.parameters)+2)
	width, _ := m.size()
	for i, p := range m.parameters {
		value := p.Value
		if value == "" {
			value = "(unset)"
		}
		scope := p.Scope
		if strings.HasPrefix(scope, "Plan ") {
			scope = strings.TrimPrefix(scope, "Plan ")
		}
		if strings.HasPrefix(scope, "Session") {
			scope = "session"
		}
		line := fmt.Sprintf("%s (%s): %s", p.Key, scope, value)
		if i == m.parameterCursor {
			line = "> " + line
		}
		lines = append(lines, truncate(line, max(1, width-2)))
	}
	if m.parameterEditing && m.parameterCursor < len(m.parameters) {
		lines = append(lines, "", "Value: "+m.dialogInput)
	}
	m.dialogLines = lines
	m.dialogKeys = []string{"↑↓ choose · Enter edit/save · Esc close"}
	_, _, _, window := m.dialogLayout(width, m.bodyHeight())
	if window > 0 {
		if m.parameterCursor < m.dialogTop {
			m.dialogTop = m.parameterCursor
		} else if m.parameterCursor >= m.dialogTop+window {
			m.dialogTop = m.parameterCursor - window + 1
		}
	}
}

func (m Model) saveCurrentParameter(value string) Model {
	if m.parameterCursor < 0 || m.parameterCursor >= len(m.parameters) {
		return m
	}
	p := m.parameters[m.parameterCursor]
	if !p.Disk {
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || (p.Key == "attempts" && n < 1) || (p.Key == "replans" && n < 0) {
			m.notice = p.Key + " must be a valid non-negative limit (attempts >= 1)"
			return m
		}
		if p.Key == "attempts" {
			m.runLimits.Attempts = n
		} else {
			m.runLimits.Replans = n
		}
		m.parameters[m.parameterCursor].Value = strconv.Itoa(n)
		m.notice = "session limits apply to the next start"
		return m
	}
	m.refreshLock()
	if reason, refused := m.lockRefusal("editing parameters"); refused {
		m.notice = reason
		return m
	}
	if m.runIsActive() {
		m.notice = "cannot edit parameters while this workspace is running"
		return m
	}
	if err := SaveParameter(m.root, p, value); err != nil {
		m.notice = err.Error()
		return m
	}
	m.notice = "parameters updated · " + parameterCommitHint(p)
	snapshot, err := m.load()
	if err != nil {
		m.notice += " · reload failed: " + err.Error()
		return m
	}
	m = m.applySnapshot(snapshotMsg{snapshot: snapshot})
	rows, err := CollectParameters(m.root, m.selectedPlan(), m.runLimits)
	if err != nil {
		m.notice += " · reload failed: " + err.Error()
	} else {
		m.parameters = rows
	}
	return m
}

// parameterCommitHint names the commit of one saved parameter: the c dialog
// takes it as the proposed message.
func parameterCommitHint(p Parameter) string {
	scope := "docflow"
	if id, ok := strings.CutPrefix(p.Scope, "Plan "); ok {
		scope = id
	}
	return fmt.Sprintf("commit: %s: set %s", scope, p.Key)
}

// Parameter is a plain screen row, independent of Bubble Tea.
type Parameter struct {
	Scope string
	Key   string
	Value string
	Path  string
	Disk  bool
}

// CollectParameters builds the project, selected-plan and session settings.
func CollectParameters(root string, plan *PlanView, limits RunLimits) ([]Parameter, error) {
	projectPath := filepath.Join(root, filepath.FromSlash(format.DocflowFilePath))
	docflow, err := format.LoadDocflow(projectPath)
	if err != nil {
		return nil, err
	}
	rows := []Parameter{
		{Scope: "Project", Key: "read_budget", Value: docflow.Meta["read_budget"], Path: projectPath, Disk: true},
		{Scope: "Project", Key: "start_read", Value: docflow.Meta["start_read"], Path: projectPath, Disk: true},
		{Scope: "Project", Key: "start_budget", Value: docflow.Meta["start_budget"], Path: projectPath, Disk: true},
	}
	if plan != nil {
		planPath := plan.Path
		if !filepath.IsAbs(planPath) {
			planPath = filepath.Join(root, filepath.FromSlash(planPath))
		}
		for _, key := range []string{"permissions", "timeout", "gate"} {
			rows = append(rows, Parameter{Scope: "Plan " + plan.ID, Key: key, Value: plan.Doc.Meta[key], Path: planPath, Disk: true})
		}
	}
	rows = append(rows,
		Parameter{Scope: "Session · next start", Key: "attempts", Value: strconv.Itoa(limits.Attempts)},
		Parameter{Scope: "Session · next start", Key: "replans", Value: strconv.Itoa(limits.Replans)},
	)
	return rows, nil
}

// SaveParameter updates one on-disk metadata value and restores the original
// bytes if the project lint rejects the resulting document.
func SaveParameter(root string, parameter Parameter, value string) error {
	if !parameter.Disk {
		return fmt.Errorf("session parameters are not written to disk")
	}
	original, err := os.ReadFile(parameter.Path)
	if err != nil {
		return err
	}
	var doc format.Doc
	if filepath.Base(parameter.Path) == "docflow.md" {
		parsed, parseErr := format.ParseDocflow(string(original), filepath.ToSlash(parameter.Path))
		if parseErr != nil {
			return parseErr
		}
		doc = parsed.Doc
	} else {
		parsed, parseErr := format.ParsePlan(string(original), filepath.ToSlash(parameter.Path))
		if parseErr != nil {
			return parseErr
		}
		doc = parsed.Doc
	}
	if _, ok := doc.MetaLines[parameter.Key]; ok {
		err = doc.SetMeta(parameter.Key, value)
	} else {
		err = doc.InsertMeta(0, parameter.Key, value)
	}
	if err != nil {
		return err
	}
	info, err := os.Stat(parameter.Path)
	if err != nil {
		return err
	}
	// An error another plan already has must not block this edit: only the
	// errors the edit introduces roll it back.
	before := map[string]bool{}
	if baseline, baselineErr := lint.Lint(root); baselineErr == nil {
		for _, issue := range baseline.Errors() {
			before[issue.String()] = true
		}
	}
	if err := os.WriteFile(parameter.Path, []byte(doc.Text()), info.Mode().Perm()); err != nil {
		return err
	}
	report, lintErr := lint.Lint(root)
	var issues []lint.Issue
	if lintErr == nil {
		for _, issue := range report.Errors() {
			if !before[issue.String()] {
				issues = append(issues, issue)
			}
		}
		if len(issues) == 0 {
			return nil
		}
	}
	rollbackErr := os.WriteFile(parameter.Path, original, info.Mode().Perm())
	if lintErr != nil {
		return errorsJoin(lintErr, rollbackErr)
	}
	var lintMessage string
	if len(issues) > 0 {
		lintMessage = issues[0].String()
		if len(issues) > 1 {
			lintMessage += fmt.Sprintf(" (and %d more errors)", len(issues)-1)
		}
	}
	return errorsJoin(fmt.Errorf("lint rejected parameter: %s", lintMessage), rollbackErr)
}

func errorsJoin(primary, rollback error) error {
	if rollback != nil {
		return fmt.Errorf("%v; rollback failed: %w", primary, rollback)
	}
	return primary
}
