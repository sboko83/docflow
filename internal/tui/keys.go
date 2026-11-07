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
	Screens []string
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
	Screens: []string{"1", "2", "3", "4"},
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
	// The dialogs own the keyboard until they close: their text is not a
	// screen switch.
	if m.dialog != dialogNone {
		return m.handleDialogKey(msg)
	}
	if key == "ctrl+c" {
		return m.requestQuit()
	}
	// The model list of the roster screen owns its letters: a and x edit the
	// globs there, they do not activate or freeze a plan. The tabs, the help
	// and the quit key stay reachable, so the list never traps the keyboard.
	if m.overlay == overlayRosterModels {
		if _, isScreen := screenForKey(key); !isScreen && key != "?" && key != "q" {
			return m.handleRosterModelsKey(key), nil
		}
	}
	if m.searching {
		return m.handleSearchKey(msg)
	}
	if m.replay != nil && replayControl(key) {
		m.notice = "Режим просмотра: управление недоступно; 1 Проект для живого прогона"
		return m, nil
	}
	if m.replay != nil && key == "esc" && m.overlay == overlayNone && m.activityQuery == "" {
		return m.leaveReplay().switchScreen(screenHistory), nil
	}
	if key == "/" {
		if m.screen == screenRun {
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
	// The screen keys work from anywhere the keyboard is not text, including
	// an open help: the help then describes the screen just chosen.
	if target, ok := screenForKey(key); ok {
		model := m.switchScreen(target)
		if model.screen == screenRoster && model.rosterPhase == rosterIdle {
			return model.startRoster()
		}
		if model.screen == screenHistory {
			return model.loadPastRuns()
		}
		return model, nil
	}
	if m.help {
		switch key {
		case "?", "esc", "q", "enter":
			m.help = false
		}
		return m, nil
	}
	switch key {
	case "q":
		return m.requestQuit()
	case "?":
		m.help = true
		return m, nil
	case "r":
		if m.screen == screenHistory {
			return m.loadPastRuns()
		}
		m.state = StateLoading
		m.errText = ""
		m.notice = reloadingText
		// The roster screen keeps its own data: reload it with the plans so
		// a file edited outside the workspace shows up too.
		if m.rosterPhase != rosterIdle {
			m = m.refreshRoster()
		}
		return m, loadCmd(m.load)
	case "tab", "shift+tab":
		return m.switchPanel(), nil
	case "s":
		model, cmd, started := m.startRun(false)
		if started && model.screen == screenProject {
			model = model.switchScreen(screenRun)
		}
		return model, cmd
	case "R":
		model, cmd, started := m.startRun(true)
		if started && model.screen == screenProject {
			model = model.switchScreen(screenRun)
		}
		return model, cmd
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
	switch m.screen {
	case screenHistory:
		return m.historyKey(key)
	case screenRoster:
		return m.handleRosterKey(key)
	case screenRun:
		if m.replay != nil && m.focus == focusTree {
			return m.replayUnitKey(key), nil
		}
		model := m.handleStreamKey(key)
		if model.screen != screenRun && model.replay != nil {
			model = model.leaveReplay().switchScreen(screenHistory)
		}
		return model, nil
	}
	if key == "A" && m.overlay == overlayNone {
		return m.openApplyDialog(), nil
	}
	if m.screen == screenProject {
		switch key {
		case "o":
			return m.openParameters(), nil
		case "b":
			return m.openBacklog(), nil
		case "N":
			m.dialog = dialogNewPlan
			m.dialogTitle = "New plan"
			m.dialogInput = ""
			m.dialogLines = []string{"Enter a slug for docflow new <slug>."}
			m.dialogKeys = []string{"Enter create · Esc cancel"}
			return m, nil
		case "e":
			return m.openPlanEditor()
		case "c":
			return m.openProjectCommitDialog()
		}
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
		return m.moveCursor(-m.paneContentHeight()), nil
	case "pgdown":
		return m.moveCursor(m.paneContentHeight()), nil
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

// handleDetailsKey scrolls and opens the card of the project screen.
func (m Model) handleDetailsKey(key string) Model {
	if m.overlay != overlayNone {
		return m.handleOverlayKey(key)
	}
	switch key {
	case "up", "k":
		if m.detailTop > 0 {
			m.detailTop--
		}
	case "down", "j":
		m.detailTop++
	case "pgup":
		m.detailTop -= m.paneContentHeight()
		if m.detailTop < 0 {
			m.detailTop = 0
		}
	case "pgdown":
		m.detailTop += m.paneContentHeight()
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
	return m.clampDetailTop()
}

// handleStreamKey navigates the run screen: the diff and verify overlays and
// the activity history of the selected unit.
func (m Model) handleStreamKey(key string) Model {
	if m.overlay != overlayNone {
		return m.handleOverlayKey(key)
	}
	if len(m.activityEntries(m.selectedUnit())) > 0 {
		return m.handleActivityKey(key)
	}
	// A unit without observations still answers the overlay keys and the way
	// back to the project screen.
	switch key {
	case "esc":
		m.screen = screenProject
		m.focus = focusTree
		return m
	case "d":
		return m.openDiff()
	case "v":
		return m.openVerify()
	}
	return m
}

// handleOverlayKey scrolls the open diff or verify overlay. The overlay owns
// the pane it was opened in: the project card or the run stream.
func (m Model) handleOverlayKey(key string) Model {
	top := m.detailTop
	if m.screen == screenRun {
		top = m.streamTop
	}
	switch key {
	case "up", "k":
		top--
	case "down", "j":
		top++
	case "pgup":
		top -= m.paneContentHeight()
	case "pgdown":
		top += m.paneContentHeight()
	case "home", "g":
		top = 0
	case "end", "G":
		top = len(m.overlayOwnerLines())
	case "[":
		if m.overlay == overlayVerify {
			m = m.loadVerifyPage(m.verifySkip - activityPageSize)
		}
	case "]":
		if m.overlay == overlayVerify {
			m = m.loadVerifyPage(m.verifySkip + activityPageSize)
		}
	case "esc", "enter", "d", "v":
		m.overlay = overlayNone
		m.diffText, m.diffErr, m.verifyText, m.verifyErr = "", "", "", ""
		m.verifyLog, m.verifySkip, m.verifyTotal = "", 0, 0
		top = 0
	}
	if top < 0 {
		top = 0
	}
	if m.screen == screenRun {
		m.streamTop = top
		return m.clampStreamTop()
	}
	m.detailTop = top
	return m.clampDetailTop()
}

// overlayOwnerLines is the overlay content in the pane that shows it.
func (m Model) overlayOwnerLines() []string {
	if m.screen == screenRun {
		return m.streamLines(m.streamWidth())
	}
	return m.detailLines(m.detailWidth())
}

// handleActivityKey navigates the bounded activity history of the selected
// unit. Plan navigation stays local to the model: it never sends a command to
// a live runner.
func (m Model) handleActivityKey(key string) Model {
	if next, ok := m.scrollActivityBlock(key); ok {
		return next
	}
	unit := m.selectedUnit()
	entries := m.activityEntries(unit)
	groups := toolGroups(entries)
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
		m.activityCursor = max(0, m.activityCursor-m.paneContentHeight())
		m.followActivity = false
	case "pgdown":
		m.activityCursor = min(last, m.activityCursor+m.paneContentHeight())
		m.followActivity = m.activityCursor == last
	case "home", "g":
		m.activityCursor = 0
		m.followActivity = false
	case "end", "G", "f":
		m.activityCursor = max(0, last)
		m.followActivity = true
	case "t":
		m.foldReasoning = !m.foldReasoning
	case "enter", "right", "l":
		return m.toggleActivity(unit, entries, groups)
	case "left", "h":
		if m.activityCursor >= 0 && m.activityCursor < len(entries) && m.expandedActivity[entryGroupKey(unit, entries, m.activityCursor, groups)] {
			delete(m.expandedActivity, entryGroupKey(unit, entries, m.activityCursor, groups))
			return m.syncActivityScroll()
		}
		m.screen = screenProject
		m.focus = focusTree
		return m
	case "[":
		return m.pageActivity(unit, entries, groups, -1)
	case "]":
		return m.pageActivity(unit, entries, groups, 1)
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
		m.screen = screenProject
		m.focus = focusTree
		return m
	}
	return m.syncActivityScroll()
}

// scrollActivityBlock lets the reader traverse a block taller than the pane
// before moving the observation cursor to its neighbour.
func (m Model) scrollActivityBlock(key string) (Model, bool) {
	step := 0
	switch key {
	case "up", "k":
		step = -1
	case "down", "j":
		step = 1
	case "pgup":
		step = -max(1, m.streamBodyHeight())
	case "pgdown":
		step = max(1, m.streamBodyHeight())
	default:
		return m, false
	}
	section := m.activitySection(m.streamWidth())
	i := m.activityCursor - section.base
	if i < 0 || i >= len(section.starts) {
		return m, false
	}
	offset := len(m.streamHeaderLines(m.streamWidth())) + 1
	start, end := offset+section.starts[i], offset+section.ends[i]
	body := max(1, m.streamBodyHeight())
	if end-start <= body || (step < 0 && m.streamTop <= start) || (step > 0 && m.streamTop+body >= end) {
		return m, false
	}
	m.streamTop = max(start, min(end-body, m.streamTop+step))
	m.followActivity = false
	return m.clampStreamTop(), true
}

func (m Model) toggleActivity(unit string, entries []ActivityEntry, groups map[int]toolPair) Model {
	if m.activityCursor < 0 || m.activityCursor >= len(entries) {
		return m
	}
	entry := entries[m.activityCursor]
	key := entryGroupKey(unit, entries, m.activityCursor, groups)
	if m.expandedActivity[key] {
		delete(m.expandedActivity, key)
		return m.syncActivityScroll()
	}
	if m.expandedActivity == nil {
		m.expandedActivity = map[string]bool{}
	}
	m.expandedActivity[key] = true
	if entry.Type == "activity" {
		// A tool call pages the result of its completed half; every other
		// activity pages its own reference. A running call keeps the live
		// window that reads the call log to its current end.
		if pair, ok := groups[m.activityCursor]; ok {
			m.loadActivityPage(toolLoadEntry(entries, pair), 0)
		} else {
			m.loadActivityPage(entry, 0)
		}
	}
	return m.syncActivityScroll()
}

func (m Model) pageActivity(unit string, entries []ActivityEntry, groups map[int]toolPair, direction int) Model {
	if m.activityCursor < 0 || m.activityCursor >= len(entries) {
		return m
	}
	index := m.activityCursor
	entry := entries[index]
	if !m.expandedActivity[entryGroupKey(unit, entries, index, groups)] || entry.Type != "activity" {
		return m
	}
	if pair, ok := groups[index]; ok {
		entry = toolLoadEntry(entries, pair)
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
	m.resetOverlayScroll()
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
	m.resetOverlayScroll()
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

// resetOverlayScroll puts an opened or closed overlay at its own start; the
// other pane keeps the offset it had.
func (m *Model) resetOverlayScroll() {
	if m.screen == screenRun {
		m.streamTop = 0
		return
	}
	m.detailTop = 0
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

// commandMods are the modifiers that turn a key press into a command instead
// of text. Shift and the lock states only choose the printed character and must
// keep it in the input.
const commandMods = tea.ModCtrl | tea.ModAlt | tea.ModMeta | tea.ModHyper | tea.ModSuper

// typedText returns the printable text a key press contributes. Bubble Tea v2
// reports Shift+A and caps lock as Text "A" with ModShift, so the text is taken
// whenever no command modifier is held instead of requiring Mod == 0.
func typedText(msg tea.KeyPressMsg) string {
	if msg.Text == "" || msg.Mod&commandMods != 0 {
		return ""
	}
	return msg.Text
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
		if text := typedText(msg); text != "" {
			if m.searchTarget == searchActivity {
				m.activityQuery += text
			} else {
				m.query += text
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
	switch m.screen {
	case screenHistory:
		if m.focus == focusTree {
			m.focus = focusDetails
		} else {
			m.focus = focusTree
		}
		return m
	case screenRoster:
		// The three panels cycle: variants, accesses, chains.
		switch m.rosterPanel {
		case panelVariants:
			m.rosterPanel = panelAccesses
		case panelAccesses:
			m.rosterPanel = panelRoles
		default:
			m.rosterPanel = panelVariants
		}
		return m
	case screenRun:
		// The stream keys keep working on either pane: the switch only says
		// which panel a narrow terminal shows.
		if m.focus == focusTree {
			m.focus = focusDetails
		} else {
			m.focus = focusTree
		}
		return m
	case screenProject:
		if m.focus == focusTree {
			m.focus = focusDetails
		} else {
			m.focus = focusTree
		}
		return m.followView()
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
