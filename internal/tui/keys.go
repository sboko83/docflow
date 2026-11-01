package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/runner"
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
	Diff    string
	Verify  string
	Follow  string
	Page    string
	Last    string
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
	Diff:    "d",
	Verify:  "v",
	Follow:  "f",
	Page:    "[]",
	Last:    "G",
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.help {
		switch key {
		case "?", "esc", "q", "enter":
			m.help = false
		}
		return m, nil
	}
	if m.dialog != dialogNone {
		return m.handleDialogKey(msg)
	}
	if key == "ctrl+c" {
		return m.requestQuit()
	}
	if m.searching {
		return m.handleSearchKey(msg)
	}
	if key == "/" {
		if m.focus == focusDetails && m.hasActivity() {
			m.searching = true
			m.searchTarget = searchActivity
			m.activityQuery = ""
			m.syncActivityCursor()
			return m, nil
		}
		m.searching = true
		m.searchTarget = searchTree
		m.query = ""
		m.recountMatches()
		m.syncCursor()
		return m, nil
	}
	switch key {
	case "q":
		return m.requestQuit()
	case "?":
		m.help = true
		return m, nil
	case "r":
		m.state = StateLoading
		m.errText = ""
		m.notice = reloadingText
		return m, loadCmd(m.load)
	case "tab":
		return m.switchPanel(), nil
	case "shift+tab":
		return m.switchPanel(), nil
	case "s":
		return m.startRun(false)
	case "R":
		return m.startRun(true)
	case "p":
		if m.controller == nil || m.controllerDone || m.runState != runner.StateRunning {
			m.notice = "Pause needs a running run"
			return m, nil
		}
		if err := m.controller.Pause(); err != nil {
			m.notice = err.Error()
			return m, nil
		}
		m.runState = runner.StatePausing
		m.notice = "pausing; the current agent call stops and the checkpoint is written"
		return m, pollSoon()
	case "S":
		if m.controller == nil || m.controllerDone || m.runState != runner.StateRunning {
			m.notice = "Stop after unit needs a running run"
			return m, nil
		}
		if err := m.controller.StopAfterUnit(); err != nil {
			m.notice = err.Error()
			return m, nil
		}
		m.runState = runner.StateStopping
		m.notice = "stopping after the current unit"
		return m, pollSoon()
	case "x":
		if m.controller == nil || m.controllerDone || m.runState != runner.StateRunning {
			m.notice = "Stop now needs a running run"
			return m, nil
		}
		if err := m.controller.StopNow(); err != nil {
			m.notice = err.Error()
			return m, nil
		}
		m.runState = runner.StateStopping
		m.notice = "stopping now"
		return m, pollSoon()
	case "a":
		return m.lifecycleKey("activate")
	case "F":
		return m.lifecycleKey("freeze")
	case "T":
		return m.lifecycleKey("thaw")
	case "C":
		return m.lifecycleKey("close")
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
	if m.overlay != overlayNone {
		switch key {
		case "up", "k":
			if m.detailTop > 0 {
				m.detailTop--
			}
		case "down", "j":
			m.detailTop++
		case "pgup":
			m.detailTop -= m.bodyHeight()
		case "pgdown":
			m.detailTop += m.bodyHeight()
		case "home", "g":
			m.detailTop = 0
		case "end", "G":
			m.detailTop = len(m.detailLines(m.detailWidth()))
		case "[":
			if m.overlay == overlayVerify {
				return m.loadVerifyPage(m.verifySkip - activityPageSize)
			}
		case "]":
			if m.overlay == overlayVerify {
				return m.loadVerifyPage(m.verifySkip + activityPageSize)
			}
		case "esc", "enter", "d", "v":
			m.overlay = overlayNone
			m.diffText, m.diffErr, m.verifyText, m.verifyErr = "", "", "", ""
			m.verifyLog, m.verifySkip, m.verifyTotal = "", 0, 0
			m.detailTop = 0
		}
		return m.clampDetailTop()
	}
	if len(m.activityEntries(m.selectedUnit())) > 0 {
		return m.handleActivityKey(key)
	}
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
	case "d":
		return m.openDiff()
	case "v":
		return m.openVerify()
	case "tab", "shift+tab":
		return m.switchPanel()
	}
	m.clampDetailTop()
	return m
}

// handleActivityKey navigates the bounded activity history of the selected
// unit. Plan navigation stays local to the model: it never sends a command to
// a live runner.
func (m Model) handleActivityKey(key string) Model {
	unit := m.selectedUnit()
	entries := m.activityEntries(unit)
	last := len(entries) - 1
	switch key {
	case "up", "k":
		if m.activityCursor > 0 {
			m.activityCursor--
		}
		m.followActivity = false
	case "down", "j":
		if m.activityCursor < last {
			m.activityCursor++
		}
		m.followActivity = m.activityCursor == last
	case "pgup":
		m.activityCursor = max(0, m.activityCursor-m.bodyHeight())
		m.followActivity = false
	case "pgdown":
		m.activityCursor = min(last, m.activityCursor+m.bodyHeight())
		m.followActivity = m.activityCursor == last
	case "home", "g":
		m.activityCursor = 0
		m.followActivity = false
	case "end", "G", "f":
		m.activityCursor = max(0, last)
		m.followActivity = true
	case "enter", "right", "l":
		return m.toggleActivity(unit, entries)
	case "left", "h":
		if m.activityCursor >= 0 && m.activityCursor < len(entries) && m.entryExpanded(unit, entries[m.activityCursor]) {
			delete(m.expandedActivity, entryKey(unit, entries[m.activityCursor]))
			return m.syncActivityScroll()
		}
		m.focus = focusTree
	case "[":
		return m.pageActivity(unit, entries, -1)
	case "]":
		return m.pageActivity(unit, entries, 1)
	case "d":
		return m.openDiff()
	case "v":
		return m.openVerify()
	case "esc":
		if strings.TrimSpace(m.activityQuery) != "" {
			m.activityQuery = ""
			m.activityCursor = 0
			m.followActivity = true
			return m.syncActivityScroll()
		}
		m.focus = focusTree
	case "tab", "shift+tab":
		return m.switchPanel()
	}
	return m.syncActivityScroll()
}

func (m Model) toggleActivity(unit string, entries []ActivityEntry) Model {
	if m.activityCursor < 0 || m.activityCursor >= len(entries) {
		return m
	}
	entry := entries[m.activityCursor]
	key := entryKey(unit, entry)
	if m.expandedActivity[key] {
		delete(m.expandedActivity, key)
		return m.syncActivityScroll()
	}
	if m.expandedActivity == nil {
		m.expandedActivity = map[string]bool{}
	}
	m.expandedActivity[key] = true
	if entry.Type == "activity" {
		m.loadActivityPage(entry, 0)
	}
	return m.syncActivityScroll()
}

func (m Model) pageActivity(unit string, entries []ActivityEntry, direction int) Model {
	if m.activityCursor < 0 || m.activityCursor >= len(entries) {
		return m
	}
	entry := entries[m.activityCursor]
	if !m.entryExpanded(unit, entry) || entry.Type != "activity" {
		return m
	}
	key := activityPageKey(unit, entry)
	skip := 0
	page := m.pages[key]
	if page != nil {
		if direction > 0 && page.Live && page.End >= page.Total {
			// The window already reached the end of the call log: reread it
			// with the length the file has now instead of advancing.
			skip = page.Skip
		} else {
			skip = page.Skip + direction*activityPageSize
			if page.Total > 0 && skip >= page.Total {
				return m
			}
		}
	}
	if skip < 0 {
		skip = 0
	}
	if direction < 0 && skip == 0 && page != nil && page.Skip == 0 {
		return m
	}
	m.loadActivityPage(entry, skip)
	return m.syncActivityScroll()
}

func (m Model) openDiff() Model {
	m.overlay = overlayDiff
	m.detailTop = 0
	text, err := ReadWorkingDiff(m.root)
	if err != nil {
		m.diffErr = err.Error()
		m.diffText = ""
		return m
	}
	m.diffText = Sanitize(text)
	return m
}

func (m Model) openVerify() Model {
	m.overlay = overlayVerify
	m.detailTop = 0
	m.verifyText, m.verifyErr = "", ""
	m.verifyLog, m.verifyRef, m.verifySkip, m.verifyTotal = "", agentio.ActivityRef{}, 0, 0
	log, ref, problem := m.verifyReference(m.selectedUnit())
	if problem != "" {
		m.verifyErr = problem
		return m
	}
	m.verifyLog, m.verifyRef = log, ref
	return m.loadVerifyPage(0)
}

// verifyReference locates the last verify event of a unit and its bounded
// stream window. A missing event or a missing log path is reported explicitly,
// never shown as an empty successful check.
func (m Model) verifyReference(unit string) (string, agentio.ActivityRef, string) {
	if m.history == nil {
		return "", agentio.ActivityRef{}, fmt.Sprintf("no verify event recorded for %s", unit)
	}
	entries := m.history.Entries(unit)
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.Type != "verify" {
			continue
		}
		log := strValue(entry.Fields["log"])
		if log == "" {
			return "", agentio.ActivityRef{}, fmt.Sprintf("verify event of %s has no log path", unit)
		}
		path, err := resolveLogPath(m.root, log)
		if err != nil {
			return "", agentio.ActivityRef{}, fmt.Sprintf("cannot read %s: %v", log, err)
		}
		// The log holds both sections; page it from the start so a failing
		// stderr tail is visible with the stdout that produced the verdict.
		ref := agentio.ActivityRef{Stream: "stdout", Offset: 0, Length: -1}
		end := -1
		for _, stream := range []string{"stdout", "stderr"} {
			part, ok := entry.Fields[stream].(map[string]any)
			if !ok {
				continue
			}
			offset, hasOffset := intValue(part["offset"])
			length, hasLength := intValue(part["length"])
			if hasOffset && hasLength && offset+length > end {
				end = offset + length
			}
		}
		if end > 0 {
			ref.Length = end
		}
		return path, ref, ""
	}
	return "", agentio.ActivityRef{}, fmt.Sprintf("no verify event recorded for %s", unit)
}

// loadVerifyPage reads one bounded window of the verify log. The full log
// stays on disk and the overlay names where the rest is.
func (m Model) loadVerifyPage(skip int) Model {
	if m.verifyLog == "" {
		return m
	}
	if skip < 0 {
		skip = 0
	}
	if m.verifyTotal > 0 && skip >= m.verifyTotal {
		return m
	}
	text, total, err := agentio.ReadActivityPage(m.verifyLog, m.verifyRef, skip, activityPageSize)
	if err != nil {
		m.verifyErr = fmt.Sprintf("%s: %v", m.verifyLog, err)
		m.verifyText = ""
		return m
	}
	m.verifyErr = ""
	m.verifySkip = skip
	m.verifyTotal = total
	page := Sanitize(text)
	if total > skip+len(text) {
		page += fmt.Sprintf("\n… %d of %d bytes shown; [ and ] page, full output: %s", skip+len(text), total, m.verifyLog)
	}
	m.verifyText = page
	return m
}

func (m Model) handleSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.searching = false
		if m.searchTarget == searchActivity {
			m.activityQuery = ""
			m.activityCursor = 0
			m.followActivity = true
			return m.syncActivityScroll(), nil
		}
		m.query = ""
		m.recountMatches()
		m.syncCursor()
		return m, nil
	case "enter":
		m.searching = false
		return m.syncActivityScroll(), nil
	case "backspace":
		if m.searchTarget == searchActivity {
			runes := []rune(m.activityQuery)
			if len(runes) > 0 {
				m.activityQuery = string(runes[:len(runes)-1])
			}
		} else {
			runes := []rune(m.query)
			if len(runes) > 0 {
				m.query = string(runes[:len(runes)-1])
			}
		}
	case "ctrl+u":
		if m.searchTarget == searchActivity {
			m.activityQuery = ""
		} else {
			m.query = ""
		}
	default:
		if msg.Text != "" && msg.Mod == 0 {
			if m.searchTarget == searchActivity {
				m.activityQuery += msg.Text
			} else {
				m.query += msg.Text
			}
		}
	}
	if m.searchTarget == searchActivity {
		m.syncActivityCursor()
		m.followActivity = true
		return m.syncActivityScroll(), nil
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
		m.followActivity = true
		m.syncActivityCursor()
		return m.syncActivityScroll()
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
