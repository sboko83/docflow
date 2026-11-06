package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

type pastRunsMsg struct {
	runs []PastRun
	err  error
}
type replayMsg struct {
	run     PastRun
	history *ActivityHistory
	err     error
}

func pastRunsCmd(root string) tea.Cmd {
	return func() tea.Msg { runs, err := LoadPastRuns(root); return pastRunsMsg{runs, err} }
}

func (m Model) leaveReplay() Model {
	if m.replay != nil {
		m.history, m.liveHistory, m.replay = m.liveHistory, nil, nil
		m = m.resetStreamReading()
		m.activityQuery, m.streamTop, m.activityCursor = "", 0, 0
		m.followActivity = true
		m.overlay = overlayNone
	}
	return m
}

func (m Model) historyKey(key string) (tea.Model, tea.Cmd) {
	if key == "enter" && len(m.pastRuns) > 0 {
		run := m.pastRuns[m.pastCursor]
		m.notice = "Чтение логов прогона…"
		return m, func() tea.Msg {
			summary, history, err := ReplayRun(m.root, run.Path)
			return replayMsg{summary, history, err}
		}
	}
	if m.focus == focusDetails {
		switch key {
		case "up", "k":
			m.pastTop = max(0, m.pastTop-1)
		case "down", "j":
			m.pastTop++
		case "pgup":
			m.pastTop = max(0, m.pastTop-m.paneContentHeight())
		case "pgdown":
			m.pastTop += m.paneContentHeight()
		case "home":
			m.pastTop = 0
		}
		return m, nil
	}
	switch key {
	case "up", "k":
		m.pastCursor--
	case "down", "j":
		m.pastCursor++
	case "pgup":
		m.pastCursor -= m.paneContentHeight()
	case "pgdown":
		m.pastCursor += m.paneContentHeight()
	case "home":
		m.pastCursor = 0
	case "end":
		m.pastCursor = len(m.pastRuns) - 1
	}
	m.pastCursor = min(max(0, m.pastCursor), max(0, len(m.pastRuns)-1))
	m.pastTop = 0
	return m, nil
}

func (m Model) historyBody(width, height int) []string {
	if len(m.pastRuns) == 0 {
		text := "Журналы прогонов лежат в <git-dir>/docflow/run; r обновить"
		if m.pastErr != "" {
			return m.messageLines(width, height, "Ошибка чтения истории", Sanitize(m.pastErr), "r повторить · ? помощь")
		}
		return m.messageLines(width, height, "пока пусто", "История прогонов", text)
	}
	list := []string{}
	top := max(0, m.pastCursor-max(0, height-2)/2)
	for i := top; i < len(m.pastRuns); i++ {
		run := m.pastRuns[i]
		marker := "  "
		if i == m.pastCursor {
			marker = "> "
		}
		list = append(list, marker+Sanitize(run.ID+" · "+run.Plan))
	}
	card := []string{}
	for _, line := range m.pastRuns[m.pastCursor].summaryLines() {
		card = append(card, wrapLine(line, max(1, width/2))...)
	}
	if m.singlePanel() {
		if m.focus == focusTree {
			return m.pane("Прогоны", list, 0, width, height, true)
		}
		card = nil
		for _, line := range m.pastRuns[m.pastCursor].summaryLines() {
			card = append(card, wrapLine(line, width)...)
		}
		return m.pane("Сводка", card[min(m.pastTop, max(0, len(card)-height+1)):], 0, width, height, true)
	}
	leftWidth := width / 2
	left := m.pane("Прогоны", list, 0, leftWidth, height, m.focus == focusTree)
	right := m.pane("Сводка", card[min(m.pastTop, max(0, len(card)-height+1)):], 0, width-leftWidth-1, height, m.focus == focusDetails)
	lines := make([]string, height)
	for i := range lines {
		lines[i] = left[i] + "│" + right[i]
	}
	return lines
}

func (m Model) replayUnitKey(key string) Model {
	index := 0
	for i, unit := range m.replay.Units {
		if unit == m.replayUnit {
			index = i
		}
	}
	switch key {
	case "up", "k":
		index--
	case "down", "j":
		index++
	case "home":
		index = 0
	case "end":
		index = len(m.replay.Units) - 1
	case "enter":
		m.focus = focusDetails
	}
	if len(m.replay.Units) > 0 {
		m.replayUnit = m.replay.Units[min(max(0, index), len(m.replay.Units)-1)]
	}
	m.activityCursor, m.streamTop = 0, 0
	m = m.resetStreamReading()
	m.followActivity = false
	return m.syncActivityScroll()
}

func (m Model) replayOverview() runOverview {
	r := m.replay
	o := runOverview{Plan: r.Plan, PlanKnown: r.Plan != "", Started: r.Started, Ended: r.Ended, HasRun: !r.Started.IsZero(), Tokens: r.Tokens, HasTokens: r.TokensKnown, UnitCount: len(r.Units), CurrentUnit: m.replayUnit, HasCurrent: m.replayUnit != ""}
	for i, id := range r.Units {
		u := m.history.units[id].run
		u.ID = id
		for _, e := range m.history.Entries(id) {
			if e.Type == "unit_end" {
				switch strValue(e.Fields["status"]) {
				case "done":
					u.Status = "x"
				case "failed", "error", "blocked":
					u.Status = "!"
				}
			}
		}
		o.Units = append(o.Units, u)
		if id == m.replayUnit {
			o.UnitIndex = i + 1
			o.Attempt = u.Attempt
			o.HasAttempt = u.HasAttempt
			o.Route, o.HasRoute = u.Route, u.HasRoute
		}
	}
	return o
}

func (m Model) replayHeader() []string {
	return []string{fmt.Sprintf("Просмотр %s · %s", Sanitize(m.replay.ID), Sanitize(m.selectedUnit())), "Tab юниты · Esc история · управление недоступно"}
}

func replayControl(key string) bool {
	switch key {
	case "s", "R", "p", "S", "x", "a", "F", "T", "C":
		return true
	}
	return false
}

func (m Model) resetStreamReading() Model {
	m.expandedActivity = map[string]bool{}
	m.pages = map[string]*activityPage{}
	m.pageOrder = nil
	return m
}
