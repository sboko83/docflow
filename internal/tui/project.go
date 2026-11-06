package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m Model) createPlan() (Model, tea.Cmd) {
	slug := strings.TrimSpace(m.dialogInput)
	if slug == "" || strings.ContainsAny(slug, " \t\r\n") {
		m.notice = "enter a plan slug"
		return m, nil
	}
	m.refreshLock()
	if notice, refused := m.lockRefusal("creating a plan is refused"); refused {
		m.notice = notice
		return m, nil
	}
	if m.runIsActive() {
		m.notice = "a live run prevents creating a plan"
		return m, nil
	}
	exe, err := os.Executable()
	if err != nil {
		m.notice = err.Error()
		return m, nil
	}
	cmd := exec.Command(exe, "new", slug)
	cmd.Dir = m.root
	load := m.load
	returnModel := m
	returnModel.dialog = dialogNone
	returnModel.notice = "creating plan"
	return returnModel, func() tea.Msg {
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			return snapshotMsg{err: fmt.Errorf("docflow new: %w: %s", runErr, strings.TrimSpace(string(out)))}
		}
		snap, loadErr := load()
		return snapshotMsg{snapshot: snap, err: loadErr}
	}
}

func (m Model) openPlanEditor() (Model, tea.Cmd) {
	plan := m.selectedPlan()
	if plan == nil {
		m.notice = "select a plan to edit"
		return m, nil
	}
	m.refreshLock()
	if notice, refused := m.lockRefusal("editing a plan is refused"); refused {
		m.notice = notice
		return m, nil
	}
	if m.runIsActive() {
		m.notice = "a live run prevents editing a plan"
		return m, nil
	}
	path := filepath.Join(m.root, filepath.FromSlash(plan.Path))
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	var cmd *exec.Cmd
	if editor == "" {
		if os.PathSeparator == '\\' {
			cmd = exec.Command("notepad", path)
		} else {
			m.notice = "$VISUAL or $EDITOR is not set"
			return m, nil
		}
	} else {
		parts := strings.Fields(editor)
		if len(parts) == 0 {
			m.notice = "editor command is empty"
			return m, nil
		}
		cmd = exec.Command(parts[0], append(parts[1:], path)...)
	}
	cmd.Dir = m.root
	load := m.load
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		snap, loadErr := load()
		if err != nil {
			return snapshotMsg{err: fmt.Errorf("editor: %w", err)}
		}
		return snapshotMsg{snapshot: snap, err: loadErr}
	})
}

func (m Model) openProjectCommitDialog() (Model, tea.Cmd) {
	files, err := projectFiles(m.root)
	if err != nil || len(files) == 0 {
		if err != nil {
			m.notice = err.Error()
		} else {
			m.notice = "no docflow files to commit"
		}
		return m, nil
	}
	msg := lastCommitMessage(m)
	lines := append([]string{"Files:"}, files...)
	lines = append(lines, "", "Message: "+msg)
	m.dialog = dialogProjectCommit
	m.dialogTitle = "Commit docflow files"
	m.dialogLines = lines
	m.dialogKeys = []string{"Enter commit · Esc cancel"}
	m.dialogInput = msg
	return m, nil
}

func (m Model) commitProjectFiles() Model {
	m.refreshLock()
	if notice, refused := m.lockRefusal("committing is refused"); refused {
		m.notice = notice
		return m
	}
	if m.runIsActive() {
		m.notice = "a live run prevents committing"
		return m
	}
	files, err := projectFiles(m.root)
	if err != nil {
		m.notice = err.Error()
		return m
	}
	if len(files) == 0 {
		m.notice = "no docflow files to commit"
		return m
	}
	message := strings.TrimSpace(m.dialogInput)
	if message == "" {
		m.notice = "commit message is empty"
		return m
	}
	add := exec.Command("git", append([]string{"add", "--"}, files...)...)
	add.Dir = m.root
	if out, err := add.CombinedOutput(); err != nil {
		m.notice = fmt.Sprintf("git add: %v: %s", err, strings.TrimSpace(string(out)))
		return m
	}
	args := append([]string{"commit", "-m", message, "--"}, files...)
	cmd := exec.Command("git", args...)
	cmd.Dir = m.root
	if out, err := cmd.CombinedOutput(); err != nil {
		m.notice = fmt.Sprintf("git commit: %v: %s", err, strings.TrimSpace(string(out)))
		return m
	}
	m.dialog = dialogNone
	m.notice = "committed docflow files"
	return m
}

func projectFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=all", "--", "docs/docflow.md", "docs/plans", "docs/archive", "docs/roster.variants.toml")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		files = append(files, strings.TrimSpace(line[3:]))
	}
	return files, nil
}

func lastCommitMessage(m Model) string {
	if plan := m.selectedPlan(); plan != nil {
		for i := len(plan.Doc.Units) - 1; i >= 0; i-- {
			if f := plan.Doc.Units[i].Field("Commit"); f != nil && strings.TrimSpace(f.Value) != "" {
				return strings.TrimSpace(f.Value)
			}
		}
	}
	return "Update project plans"
}
