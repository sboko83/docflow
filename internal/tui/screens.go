package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// screenForKey maps the number keys 1-4 to the workspace screens.
func screenForKey(key string) (screen, bool) {
	for i, candidate := range screens {
		if key == fmt.Sprintf("%d", i+1) {
			return candidate, true
		}
	}
	return screenProject, false
}

// switchScreen moves the workspace to another screen. Only the run screen
// owns a scroll of its own here: the project card keeps its offset while the
// stream is read.
func (m Model) switchScreen(target screen) Model {
	if m.screen == target {
		return m
	}
	if target != screenRun {
		m = m.leaveReplay()
	}
	if target != screenRoster && m.overlay == overlayRosterModels {
		// The model list belongs to the roster screen: another screen has no
		// pane that could show it.
		m.overlay = overlayNone
		m.rmodels = nil
	}
	m.screen = target
	switch target {
	case screenRun:
		// The stream is what the reader comes for: the units pane is the
		// summary beside it, reached with Tab.
		m.focus = focusDetails
		return m.syncActivityScroll()
	case screenProject:
		// The tree is where a screen switch lands: the stream and the
		// placeholders have no panel of their own to keep focus on.
		m.focus = focusTree
	case screenHistory:
		m.focus = focusTree
	}
	return m
}

// tabsLine renders the screen tabs with the active one marked. The run host
// state stays on the right. On a narrow terminal the tab labels give way to
// their numbers, keeping the active title: the names are one keypress away.
func (m Model) tabsLine(width int) string {
	styles := newStyles(m.noColor)
	full := make([]string, len(screens))
	numbers := make([]string, len(screens))
	for i, candidate := range screens {
		full[i] = fmt.Sprintf("%d %s", i+1, candidate.title())
		numbers[i] = fmt.Sprintf("%d", i+1)
		if candidate == m.screen {
			// The compact row keeps the active title: the numbers alone do
			// not say where the reader is.
			numbers[i] = full[i]
		}
	}
	build := func(items []string) (string, int) {
		parts := make([]string, len(items))
		visible := 0
		for i, item := range items {
			if screens[i] == m.screen {
				parts[i] = styles.picked.Render("[" + item + "]")
				visible += lipgloss.Width(item) + 2
				continue
			}
			parts[i] = styles.dim.Render(item)
			visible += lipgloss.Width(item)
		}
		visible += 2 * (len(items) - 1)
		return strings.Join(parts, "  "), visible
	}
	stateText := "run:" + m.stateOfRun()
	left, leftWidth := build(full)
	if leftWidth+2+lipgloss.Width(stateText) > width {
		left, leftWidth = build(numbers)
	}
	gap := width - leftWidth - lipgloss.Width(stateText)
	if gap < 2 {
		gap = 2
	}
	return padLine(truncate(left+strings.Repeat(" ", gap)+styles.dim.Render(stateText), width), width)
}

// stateOfRun is the unrendered host state for width arithmetic.
func (m Model) stateOfRun() string {
	if m.runState == "" {
		return "idle"
	}
	return string(m.runState)
}

// keysLine is the footer of the current screen: its own keys, at most five,
// and the way to the full keyboard. The keys shared by every screen are in
// the help, not here.
func (m Model) keysLine(width int) string {
	var keys []string
	switch m.screen {
	case screenProject:
		keys = []string{"↑↓ move", "Enter open", "o settings", "Tab panel", "s start"}
	case screenRoster:
		if m.overlay == overlayRosterModels {
			keys = []string{"↑↓ move", "Esc close"}
			break
		}
		// A roster without a policy or a catalog has no panels to navigate:
		// its footer names the action the body asks for.
		switch {
		case m.roster != nil && m.roster.Legacy:
			keys = []string{"Enter migrate", "u scan"}
		case m.roster != nil && (m.roster.Missing || m.roster.CatalogEmpty):
			keys = []string{"u scan"}
		default:
			keys = []string{"↑↓ move", "Tab panel", "Space on/off", "Enter models"}
		}
	case screenRun:
		keys = []string{"↑↓ move", "Enter expand", "[ ] page", "Tab panel", "t fold"}
	case screenHistory:
		keys = []string{"↑↓ move", "Enter replay", "Tab panel", "r reload"}
	}
	keys = append(keys, "? помощь")
	// A narrow footer drops keys from the right but never the help: it is the
	// way to the full keyboard.
	for len(keys) > 1 && lipgloss.Width(strings.Join(keys, " · ")) > width {
		keys = append(keys[:len(keys)-2], keys[len(keys)-1])
	}
	return strings.Join(keys, " · ")
}

// screenHelp is the per-screen block of the help.
func (m Model) screenHelp() []string {
	switch m.screen {
	case screenRoster:
		return []string{
			"↑/k, ↓/j     строка вариантов или доступа; на цепочках — прокрутка",
			"PgUp/PgDn, Home/End  страница и края списка или цепочек",
			"Tab          панель: варианты · доступы · цепочки",
			"u            собрать каталог: roster scan без --online, в фоне",
			"",
			"На доступе:",
			"Space        включить или выключить; диалог спрашивает дату",
			"             «off --until»: пусто — бессрочно",
			"Enter        модели доступа из каталога и роли, которые они",
			"             обслуживают; ячейки правит docflow roster web",
			"",
			"На варианте:",
			"Enter        сделать активным пресетом",
			"Backspace    снять пресет",
			"n            сохранить условия как новый вариант",
			"D            удалить вариант машинного файла",
			"A            применить вариант к выбранному на «Проекте» плану:",
			"             разница шапки, Enter пишет её как roster apply",
			"Esc          вернуться на «Проект»",
			"",
			"Без каталога панель цепочек зовёт `u — собрать каталог`; пустой",
			"roster.toml экран не создаёт — он называет файл и пример из",
			"docs/design/roster.md. Ростер старой версии открывает Enter:",
			"предпросмотр миграции, Enter пишет файлы с бэкапом .bak, Esc — нет.",
			"",
			"Экран читает roster.LoadWorkspace: политика, варианты и условия",
			"проекта. Правка пишет roster.state.toml или файл вариантов и",
			"перечитывает модель; отказ показывается в notice, файл остаётся.",
		}
	case screenHistory:
		return []string{
			"↑↓ / PgUp/PgDn / Home/End select a past run, newest first.",
			"Tab switches the list and its scrollable summary; Enter replays the stream.",
			"Replay: Tab then ↑↓ selects a unit; Esc returns to history.",
			"Run controls refuse in replay; 1 returns to the live workspace. r reloads journals.",
		}
	case screenRun:
		return []string{
			"↑/k, ↓/j     move through the observations of the selected unit",
			"PgUp/PgDn    move one page; Home/End first or last observation",
			"Enter        expand or collapse an observation",
			"[ ]          page a large result or message from its call log",
			"t            fold every reasoning block to its first line and back",
			"f, End, G    follow the newest observation; G jumps to the last",
			"/            search the stream; Esc clears the search",
			"Tab          the stream and the units pane; narrow: the one panel",
			"d            show the actual git diff of the working tree",
			"v            show the last verify log of the selected unit",
			"Esc          close the overlay or return to the Проект screen",
			"",
			"The line above the panes sums the run: the plan, the run time, the",
			"unit n/m, the attempt k/N, the tokens of the usage observations",
			"(unknown as ?, never a zero) and the current route. The left pane",
			"lists the units with status, duration and route, and the stages of",
			"the current unit the runner reached: prompt · work · verify · done ·",
			"commit. Reasoning reads in full, dimmed; a tool call is one line and",
			"Enter opens its result by pages; ✎ names a changed file. The stream",
			"belongs to the unit selected on the Проект screen; press 1, then 3.",
		}
	}
	return []string{
		"↑/k, ↓/j     move the selection",
		"PgUp/PgDn    move one page; Home/End first or last item",
		"Enter        expand a plan, phase or unit; open the unit card",
		"←/h, →/l     collapse or expand the selected plan, phase or unit",
		"Tab          switch the tree and the card",
		"/            search plans, phases and units",
		"s            start the current Queue run and open Прогон",
		"R            resume the saved checkpoint",
		"a, F, T, C   activate · freeze · thaw · close the plan",
		"N            create a draft with docflow new <slug>",
		"o            edit project, selected plan and next-session parameters",
		"b            Backlog: + add · Enter edit · D delete · P plan with agent",
		"e            open the selected plan in VISUAL or EDITOR",
		"c            commit only docflow files with the latest Commit hint",
		"A            apply the active roster variant to the plan header:",
		"             the difference is shown first, Enter writes it",
		"d, v         in the card: git diff · the last verify log of the unit",
		"Esc          close search, dialog, overlay or help; return to the tree",
	}
}

// placeholderLines renders a screen that has no data yet: the empty state and
// where the same work lives today.
func (m Model) placeholderLines(width, height int) []string {
	hint := "История пока пуста: прошлые прогоны и повтор потока появятся здесь."
	footer := "Журналы прогонов лежат в <git-dir>/docflow/run; ? помощь"
	return m.messageLines(width, height, "пока пусто", hint, footer)
}

// streamWidth is the width of the stream pane content. The run screen shows
// the units of the plan beside the stream from 100 columns; below that the
// stream is the one panel and Tab switches to the units.
func (m Model) streamWidth() int {
	width, _ := m.size()
	if m.singlePanel() {
		return width
	}
	left := width * 2 / 5
	if left < 24 {
		left = 24
	}
	if width-left-1 < minWidth/2 {
		left = width * 3 / 5
	}
	return width - left - 1
}

// streamHeaderLines is everything above the observation list on the run
// screen; the stream scroll offset counts from here.
func (m Model) streamHeaderLines(width int) []string {
	if m.replay != nil {
		return m.replayHeader()
	}
	styles := newStyles(m.noColor)
	selected, ok := m.selectedRow()
	if !ok || selected.kind != rowUnit {
		return []string{
			styles.title.Render("No unit selected"),
			styles.dim.Render("Select a unit on the Проект screen with 1, then open the stream with 3."),
		}
	}
	follow := "following the newest observation"
	if !m.followActivity {
		follow = "paused: new events do not move the view"
	}
	return []string{
		styles.title.Render(fmt.Sprintf("%s %s", selected.unit.ID, selected.unit.Title)),
		styles.dim.Render(fmt.Sprintf("%s · %d in the stream · Enter expands an entry · [ ] pages a large result · / searches",
			follow, len(m.activityEntries(selected.unit.ID)))),
	}
}

// streamLines renders the run screen: the live stream of the selected unit
// and, while an overlay is open, the overlay instead.
func (m Model) streamLines(width int) []string {
	if m.overlay != overlayNone {
		return m.overlayLines(width)
	}
	lines := append([]string{}, m.streamHeaderLines(width)...)
	section := m.activitySection(width)
	if len(section.lines) == 0 {
		lines = append(lines, "", "No observations for this unit yet.",
			"Start a run with s; the stream of the running unit appears here.")
		return lines
	}
	lines = append(lines, "")
	return append(lines, section.lines...)
}

// visibleStreamLines applies the stream scroll offset.
func (m Model) visibleStreamLines(width int) []string {
	lines := m.streamLines(width)
	offset := m.streamTop
	if offset < 0 {
		offset = 0
	}
	if maxTop := m.maxStreamTop(lines); offset > maxTop {
		offset = maxTop
	}
	return lines[offset:]
}

// streamBodyHeight is the height of the stream pane content on the run
// screen: the pane title and the run summary line above the panes are not
// stream lines, and every scroll step of the stream uses it.
func (m Model) streamBodyHeight() int {
	width, _ := m.size()
	return max(0, m.paneContentHeight()-len(m.runHeaderLines(width, m.bodyHeight())))
}

// maxStreamTop is the last scroll offset that still leaves content in the
// stream pane.
func (m Model) maxStreamTop(lines []string) int {
	maxTop := len(lines) - m.streamBodyHeight()
	if maxTop < 0 {
		maxTop = 0
	}
	return maxTop
}

func (m Model) clampStreamTop() Model {
	maxTop := m.maxStreamTop(m.streamLines(m.streamWidth()))
	if m.streamTop > maxTop {
		m.streamTop = maxTop
	}
	if m.streamTop < 0 {
		m.streamTop = 0
	}
	return m
}
