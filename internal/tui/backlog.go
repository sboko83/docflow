package tui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/format"
)

func (m Model) openBacklog() Model {
	doc, err := format.LoadDocflow(filepath.Join(m.root, format.DocflowFilePath))
	if err != nil {
		m.notice = err.Error()
		return m
	}
	m.backlog = doc
	m.backlogCursor = max(0, min(m.backlogCursor, len(doc.Backlog)-1))
	return m.showBacklog()
}

func (m Model) showBacklog() Model {
	m.dialog = dialogBacklog
	m.dialogTitle = "Backlog"
	m.dialogKeys = []string{"+ add · Enter edit · D delete", "P plan with agent · Esc back"}
	m.dialogLines = nil
	if len(m.backlog.Backlog) == 0 {
		m.dialogLines = []string{"Backlog is empty. Press + to add a task."}
	}
	for i, item := range m.backlog.Backlog {
		prefix := "  "
		if i == m.backlogCursor {
			prefix = "> "
		}
		m.dialogLines = append(m.dialogLines, prefix+Sanitize(item.Text))
	}
	// Keep the start of the selected item visible, including at 32x10.
	width, _ := m.size()
	m.dialogTop = 0
	for _, line := range m.dialogLines[:min(m.backlogCursor, len(m.dialogLines))] {
		m.dialogTop += len(wrapLine(line, max(1, width-2)))
	}
	return m
}

func (m Model) backlogWritable() (Model, bool) {
	m.refreshLock()
	if notice, refused := m.lockRefusal("editing Backlog is refused"); refused {
		m.notice = notice
		return m, false
	}
	if m.runIsActive() {
		m.notice = "a live run prevents editing Backlog or planning"
		return m, false
	}
	return m, true
}

func (m Model) editBacklog(index int) Model {
	m.backlogEdit = index
	m.dialogInput = ""
	if index >= 0 {
		text, err := m.backlog.BacklogText(index)
		if err != nil {
			m.notice = err.Error()
			return m
		}
		m.dialogInput = text
	}
	m.dialog = dialogBacklogEdit
	m.dialogTitle = "Edit Backlog task"
	m.dialogKeys = []string{"Enter newline · Ctrl+S save", "arrows move · Esc cancel"}
	m.backlogCaret = utf8.RuneCountInString(m.dialogInput)
	return m.backlogEditorLines()
}

func (m Model) backlogEditorLines() Model {
	runes := []rune(m.dialogInput)
	m.backlogCaret = max(0, min(m.backlogCaret, len(runes)))
	before := Sanitize(string(runes[:m.backlogCaret]))
	after := Sanitize(string(runes[m.backlogCaret:]))
	m.dialogLines = strings.Split(before+"▏"+after, "\n")
	width, _ := m.size()
	caretLine := 0
	for _, line := range strings.Split(before, "\n") {
		caretLine += len(wrapLine(line, max(1, width-2)))
	}
	_, _, _, window := m.dialogLayout(width, m.bodyHeight())
	if caretLine-1 < m.dialogTop {
		m.dialogTop = max(0, caretLine-1)
	} else if caretLine > m.dialogTop+window {
		m.dialogTop = max(0, caretLine-window)
	}
	return m
}

// currentBacklog refuses stale edits rather than overwriting a concurrent
// change to Queue, another task, or the task being planned.
func (m Model) currentBacklog() (*format.DocflowFile, error) {
	doc, err := format.LoadDocflow(filepath.Join(m.root, format.DocflowFilePath))
	if err != nil {
		return nil, err
	}
	if m.backlog == nil || doc.Text() != m.backlog.Text() {
		return nil, fmt.Errorf("docflow.md changed; cancel and reopen Backlog")
	}
	return doc, nil
}

func (m Model) saveBacklog(deleting bool) (Model, tea.Cmd) {
	var ok bool
	if m, ok = m.backlogWritable(); !ok {
		return m, nil
	}
	text := m.dialogInput
	index := m.backlogEdit
	if deleting {
		text, index = "", m.backlogCursor
	} else if strings.TrimSpace(text) == "" {
		m.notice = "task is empty; use D to delete it"
		return m, nil
	}
	doc, err := m.currentBacklog()
	if err == nil {
		err = doc.EditBacklog(index, text)
	}
	if err == nil {
		err = doc.Save("")
	}
	if err != nil {
		m.notice = err.Error()
		return m, nil
	}
	m.notice = "Backlog saved; commit project files before starting a run"
	m = m.openBacklog()
	return m, loadCmd(m.load)
}

func (m Model) planningRefused(err error, prompt string) Model {
	m.planningPrompt = prompt
	m.dialog = dialogPlanningPrompt
	m.dialogTitle = "Planning unavailable"
	m.dialogLines = []string{Sanitize(err.Error()), "Copy this prompt into an interactive agent:", Sanitize(prompt)}
	m.dialogKeys = []string{"y copy prompt · ↑/↓ scroll", "Esc back"}
	m.dialogTop = 0
	return m
}

func (m Model) planBacklog() (Model, tea.Cmd) {
	text, err := m.backlog.BacklogText(m.backlogCursor)
	if err != nil {
		m.notice = err.Error()
		return m, nil
	}
	prompt := "возьми строку Backlog в план по скиллу `bsv-docflow-plan`: " + text
	var ok bool
	if m, ok = m.backlogWritable(); !ok {
		return m, nil
	}
	if _, err = m.currentBacklog(); err != nil {
		return m.planningRefused(err, prompt), nil
	}
	// Conditions may have changed since the roster screen was opened.
	m = m.refreshRoster()
	if m.roster == nil {
		return m.planningRefused(fmt.Errorf("cannot load roster: %s", m.rosterErr), prompt), nil
	}
	route, err := m.roster.PlanningRoute()
	if err != nil {
		return m.planningRefused(err, prompt), nil
	}
	argv, err := agents.InteractiveArgv(route, prompt)
	if err != nil {
		return m.planningRefused(err, prompt), nil
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return m.planningRefused(err, prompt), nil
	}
	cmd := exec.Command(path, argv[1:]...)
	cmd.Dir = m.root
	if m, ok = m.backlogWritable(); !ok {
		return m, nil
	}
	m.dialog = dialogNone
	load, rosterLoad := m.load, m.rosterLoad
	root, rosterPath := m.root, m.rosterPath
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		snap, loadErr := load()
		workspace, rosterErr := rosterLoad(rosterPath, root)
		return planningDoneMsg{snapshotMsg{snapshot: snap, err: loadErr}, rosterMsg{workspace: workspace, err: rosterErr}, err, prompt}
	})
}

type planningDoneMsg struct {
	snapshot snapshotMsg
	roster   rosterMsg
	err      error
	prompt   string
}

func (m Model) handleBacklogKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		m.dialog = dialogNone
		return m.requestQuit()
	}
	if m.dialog == dialogPlanningPrompt {
		if key == "y" {
			return m, tea.SetClipboard(m.planningPrompt)
		}
		if key == "esc" {
			return m.openBacklog(), nil
		}
		m, _ = m.scrollDialog(key)
		return m, nil
	}
	if m.dialog == dialogBacklogDelete {
		switch key {
		case "enter":
			return m.saveBacklog(true)
		case "esc":
			return m.showBacklog(), nil
		}
		m, _ = m.scrollDialog(key)
		return m, nil
	}
	if m.dialog == dialogBacklogEdit {
		runes := []rune(m.dialogInput)
		pos := m.backlogCaret
		insert := ""
		switch key {
		case "esc":
			return m.showBacklog(), nil
		case "ctrl+s":
			return m.saveBacklog(false)
		case "enter":
			insert = "\n"
		case "left":
			pos = max(0, pos-1)
		case "right":
			pos = min(len(runes), pos+1)
		case "home", "ctrl+a":
			pos = 0
		case "end", "ctrl+e":
			pos = len(runes)
		case "up", "down":
			start := pos
			for start > 0 && runes[start-1] != '\n' {
				start--
			}
			column := pos - start
			if key == "up" && start > 0 {
				prev := start - 1
				for prev > 0 && runes[prev-1] != '\n' {
					prev--
				}
				pos = min(prev+column, start-1)
			} else if key == "down" {
				next := pos
				for next < len(runes) && runes[next] != '\n' {
					next++
				}
				if next < len(runes) {
					next++
					end := next
					for end < len(runes) && runes[end] != '\n' {
						end++
					}
					pos = min(next+column, end)
				}
			}
		case "backspace":
			if pos > 0 {
				runes = append(runes[:pos-1], runes[pos:]...)
				pos--
			}
		case "delete":
			if pos < len(runes) {
				runes = append(runes[:pos], runes[pos+1:]...)
			}
		default:
			insert = typedText(msg)
		}
		if insert != "" {
			runes = append(runes[:pos], append([]rune(insert), runes[pos:]...)...)
			pos += utf8.RuneCountInString(insert)
		}
		m.dialogInput, m.backlogCaret = string(runes), pos
		return m.backlogEditorLines(), nil
	}
	if key == "esc" {
		m.dialog = dialogNone
		return m, nil
	}
	if key == "+" {
		return m.editBacklog(-1), nil
	}
	if len(m.backlog.Backlog) == 0 {
		return m, nil
	}
	switch key {
	case "up", "k":
		m.backlogCursor--
	case "down", "j":
		m.backlogCursor++
	case "home":
		m.backlogCursor = 0
	case "end":
		m.backlogCursor = len(m.backlog.Backlog) - 1
	case "pgup":
		m.backlogCursor -= max(1, m.bodyHeight()-4)
	case "pgdown":
		m.backlogCursor += max(1, m.bodyHeight()-4)
	case "enter":
		return m.editBacklog(m.backlogCursor), nil
	case "P":
		return m.planBacklog()
	case "D":
		m.dialog = dialogBacklogDelete
		m.dialogTitle = "Delete Backlog task?"
		m.dialogLines = []string{Sanitize(m.backlog.Backlog[m.backlogCursor].Text)}
		m.dialogKeys = []string{"Enter delete · Esc keep"}
		m.dialogTop = 0
		return m, nil
	}
	m.backlogCursor = max(0, min(m.backlogCursor, len(m.backlog.Backlog)-1))
	return m.showBacklog(), nil
}

func (m Model) insertBacklogText(text string) Model {
	runes := []rune(m.dialogInput)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	pos := m.backlogCaret
	m.dialogInput = string(runes[:pos]) + text + string(runes[pos:])
	m.backlogCaret += utf8.RuneCountInString(text)
	return m.backlogEditorLines()
}
