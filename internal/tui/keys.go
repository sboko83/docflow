package tui

import (
	tea "charm.land/bubbletea/v2"
)

// KeyMap is the keyboard contract shown by help and exercised by tests.
type KeyMap struct {
	Move    []string
	Expand  []string
	Panel   []string
	Search  string
	Help    string
	Refresh string
	Quit    []string
	Back    string
}

// Keys is the workspace key map.
var Keys = KeyMap{
	Move:    []string{"up", "down", "k", "j", "pgup", "pgdown", "home", "end"},
	Expand:  []string{"enter", "right", "left"},
	Panel:   []string{"tab", "shift+tab"},
	Search:  "/",
	Help:    "?",
	Refresh: "r",
	Quit:    []string{"q", "ctrl+c"},
	Back:    "esc",
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if m.help {
		switch key {
		case "?", "esc", "q", "enter":
			m.help = false
		}
		return m, nil
	}
	if m.searching {
		return m.handleSearchKey(msg)
	}
	switch key {
	case "q":
		return m, tea.Quit
	case "?":
		m.help = true
		return m, nil
	case "/":
		m.searching = true
		m.query = ""
		m.recountMatches()
		m.syncCursor()
		return m, nil
	case "r":
		m.state = StateLoading
		m.errText = ""
		m.notice = "reloading"
		return m, loadCmd(m.load)
	case "tab":
		return m.switchPanel(), nil
	case "shift+tab":
		return m.switchPanel(), nil
	}
	if m.focus == focusDetails {
		return m.handleDetailsKey(key), nil
	}
	switch key {
	case "up", "k":
		return m.moveCursor(-1), nil
	case "down", "j":
		return m.moveCursor(1), nil
	case "pgup":
		return m.moveCursor(-m.bodyHeight()), nil
	case "pgdown":
		return m.moveCursor(m.bodyHeight()), nil
	case "home", "g":
		return m.setCursor(0), nil
	case "end", "G":
		return m.setCursor(len(m.rows()) - 1), nil
	case "enter":
		return m.activateRow(), nil
	case "right", "l":
		return m.expandRow(), nil
	case "left", "h":
		return m.collapseRow(), nil
	}
	return m, nil
}

func (m Model) handleDetailsKey(key string) Model {
	switch key {
	case "up", "k":
		if m.detailTop > 0 {
			m.detailTop--
		}
	case "down", "j":
		m.detailTop++
	case "pgup":
		m.detailTop -= m.bodyHeight()
		if m.detailTop < 0 {
			m.detailTop = 0
		}
	case "pgdown":
		m.detailTop += m.bodyHeight()
	case "home", "g":
		m.detailTop = 0
	case "esc", "left", "h", "enter":
		m.focus = focusTree
	case "tab", "shift+tab":
		return m.switchPanel()
	}
	m.clampDetailTop()
	return m
}

func (m Model) handleSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.searching = false
		m.query = ""
		m.recountMatches()
		m.syncCursor()
		return m, nil
	case "enter":
		m.searching = false
		return m, nil
	case "backspace":
		runes := []rune(m.query)
		if len(runes) > 0 {
			m.query = string(runes[:len(runes)-1])
		}
	case "ctrl+u":
		m.query = ""
	default:
		if msg.Text != "" && msg.Mod == 0 {
			m.query += msg.Text
		}
	}
	m.recountMatches()
	m.syncCursor()
	return m, nil
}

func (m Model) switchPanel() Model {
	if m.singlePanel() {
		if m.focus == focusTree {
			m.focus = focusDetails
		} else {
			m.focus = focusTree
		}
		return m
	}
	if m.focus == focusTree {
		m.focus = focusDetails
	} else {
		m.focus = focusTree
	}
	return m
}

func (m Model) activateRow() Model {
	selected, ok := m.selectedRow()
	if !ok {
		return m
	}
	if selected.kind == rowUnit {
		m.focus = focusDetails
		m.detailTop = 0
		return m
	}
	return m.toggle(selected.key)
}

func (m Model) expandRow() Model {
	selected, ok := m.selectedRow()
	if !ok {
		return m
	}
	if m.isExpanded(selected.key) {
		return m.moveCursor(1)
	}
	if m.expanded == nil {
		m.expanded = map[string]bool{}
	}
	m.expanded[selected.key] = true
	m.syncCursor()
	return m
}

func (m Model) collapseRow() Model {
	selected, ok := m.selectedRow()
	if !ok {
		return m
	}
	if m.isExpanded(selected.key) && selected.kind != rowUnit {
		if m.expanded == nil {
			m.expanded = map[string]bool{}
		}
		m.expanded[selected.key] = false
		m.syncCursor()
		return m
	}
	return m.parentCursor()
}
