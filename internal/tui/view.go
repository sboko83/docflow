package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"bsv-docflow/internal/format"
)

const (
	minWidth  = 32
	minHeight = 10
)

type viewStyles struct {
	noColor bool
	title   lipgloss.Style
	dim     lipgloss.Style
	section lipgloss.Style
	plan    lipgloss.Style
	phase   lipgloss.Style
	unit    lipgloss.Style
	done    lipgloss.Style
	blocked lipgloss.Style
	doing   lipgloss.Style
	picked  lipgloss.Style
}

func newStyles(noColor bool) viewStyles {
	if noColor {
		return viewStyles{noColor: true}
	}
	return viewStyles{
		title:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7dd3fc")),
		dim:     lipgloss.NewStyle().Foreground(lipgloss.Color("#9ca3af")),
		section: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#c084fc")),
		plan:    lipgloss.NewStyle().Bold(true),
		phase:   lipgloss.NewStyle().Foreground(lipgloss.Color("#fbbf24")),
		unit:    lipgloss.NewStyle(),
		done:    lipgloss.NewStyle().Foreground(lipgloss.Color("#4ade80")),
		blocked: lipgloss.NewStyle().Foreground(lipgloss.Color("#f87171")),
		doing:   lipgloss.NewStyle().Foreground(lipgloss.Color("#60a5fa")),
		picked:  lipgloss.NewStyle().Reverse(true),
	}
}

// View renders the full-screen workspace.
func (m Model) View() tea.View {
	view := tea.NewView(m.render())
	view.AltScreen = true
	view.WindowTitle = "docflow tui"
	return view
}

// String renders the view content. Tests use it directly; View wraps it for
// Bubble Tea.
func (m Model) String() string { return m.render() }

func (m Model) render() string {
	width := m.width
	height := m.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	if tooSmall(width, height) {
		return m.renderTooSmall(width, height)
	}
	styles := newStyles(m.noColor)
	header := []string{
		padLine(styles.title.Render("docflow tui")+"  "+styles.dim.Render(truncate(m.root, max(0, width-14))), width),
		strings.Repeat("─", width),
	}
	footer := []string{
		strings.Repeat("─", width),
		padLine(styles.dim.Render(m.statusLine()), width),
		padLine(styles.dim.Render(keysLine()), width),
	}
	bodyHeight := height - len(header) - len(footer)
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	var body []string
	switch m.state {
	case StateLoading:
		body = m.messageLines(width, bodyHeight, "Loading plan workspace...", "Reading docs/docflow.md and queue plans.", "r reload · q quit")
	case StateEmpty:
		body = m.messageLines(width, bodyHeight, "No plans in the queue.", "Create one with docflow new <slug> or activate a draft.", "r reload · ? help · q quit")
	case StateError:
		body = m.messageLines(width, bodyHeight, "Cannot load the plan workspace.", m.errText, "r retry · ? help · q quit")
	default:
		if m.help {
			body = m.helpLines(width, bodyHeight)
		} else if m.singlePanel() {
			if m.focus == focusDetails {
				body = m.pane("Details", m.visibleDetailLines(width), 0, width, bodyHeight, true)
			} else {
				body = m.pane("Tree", m.treeLines(width), 0, width, bodyHeight, true)
			}
		} else {
			leftWidth := width * 2 / 5
			if leftWidth < 24 {
				leftWidth = 24
			}
			if width-leftWidth-1 < minWidth/2 {
				leftWidth = width * 3 / 5
			}
			rightWidth := width - leftWidth - 1
			left := m.pane("Tree", m.treeLines(leftWidth), 0, leftWidth, bodyHeight, m.focus == focusTree)
			right := m.pane("Details", m.visibleDetailLines(rightWidth), 0, rightWidth, bodyHeight, m.focus == focusDetails)
			body = make([]string, bodyHeight)
			for i := 0; i < bodyHeight; i++ {
				body[i] = left[i] + "│" + right[i]
			}
		}
	}
	lines := append(header, body...)
	lines = append(lines, footer...)
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

func tooSmall(width, height int) bool { return width < minWidth || height < minHeight }

func (m Model) renderTooSmall(width, height int) string {
	styles := newStyles(m.noColor)
	lines := []string{
		strings.Repeat(" ", width),
		padLine(styles.title.Render("Terminal too small"), width),
		padLine(fmt.Sprintf("Need at least %dx%d, have %dx%d.", minWidth, minHeight, width, height), width),
		padLine("Resize the terminal or press q to quit.", width),
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

func (m Model) messageLines(width, height int, lines ...string) []string {
	out := []string{" "}
	for _, line := range lines {
		for _, wrapped := range wrapLine(line, max(1, width-2)) {
			out = append(out, " "+wrapped)
		}
	}
	for len(out) < height {
		out = append(out, "")
	}
	if len(out) > height {
		out = out[:height]
	}
	for i := range out {
		out[i] = padLine(out[i], width)
	}
	return out
}

func (m Model) helpLines(width, height int) []string {
	styles := newStyles(m.noColor)
	lines := []string{
		styles.title.Render("Keyboard and workspace"),
		"",
		"↑/k, ↓/j     move the selection",
		"PgUp/PgDn    move one page",
		"Home/End     first or last item",
		"Enter        expand or collapse; open details of a unit",
		"←/h, →/l     collapse or expand the selected plan, phase or unit",
		"Tab          switch focus; on a narrow terminal switch panel",
		"/            search plans, phases and units; in details search activity",
		"Enter        expand an activity entry in details",
		"[ ]          page a large result or message from its call log",
		"f, End       follow the newest activity; G jumps to the last event",
		"d            show the actual git diff of the working tree",
		"v            show the last verify log of the selected unit",
		"Esc          close search, overlay, help or return to the tree",
		"r            reload the workspace from disk",
		"?            this help",
		"q, Ctrl+C    quit",
		"",
		"The workspace is read-only: it reads plans with the docflow parser",
		"and never edits the plan, runs an agent or takes a lock.",
	}
	var out []string
	for _, line := range lines {
		for _, wrapped := range wrapLine(line, max(1, width-2)) {
			out = append(out, padLine(" "+wrapped, width))
		}
	}
	for len(out) < height {
		out = append(out, strings.Repeat(" ", width))
	}
	if len(out) > height {
		out = out[:height]
	}
	return out
}

func (m Model) treeLines(width int) []string {
	rows := m.rows()
	if len(rows) == 0 {
		message := "No matching plans or units."
		if strings.TrimSpace(m.query) == "" {
			message = "No plans to show."
		}
		return []string{truncate(message, width)}
	}
	height := m.bodyHeight()
	offset := m.treeScroll
	if m.cursor < offset {
		offset = m.cursor
	}
	if height > 0 && m.cursor >= offset+height {
		offset = m.cursor - height + 1
	}
	if offset < 0 {
		offset = 0
	}
	if offset > len(rows)-1 {
		offset = max(0, len(rows)-1)
	}
	styles := newStyles(m.noColor)
	lines := make([]string, 0, height)
	for i := offset; i < len(rows) && len(lines) < height; i++ {
		item := rows[i]
		marker := " "
		if i == m.cursor {
			marker = ">"
		}
		glyph := ""
		if item.kind != rowUnit {
			if m.isExpanded(item.key) {
				glyph = "▾ "
			} else {
				glyph = "▸ "
			}
		}
		line := marker + " " + strings.Repeat("  ", item.depth) + glyph + item.label
		line = padLine(truncate(line, width), width)
		style := styles.unit
		switch item.kind {
		case rowSection:
			style = styles.section
		case rowPlan:
			style = styles.plan
		case rowPhase:
			style = styles.phase
		case rowUnit:
			switch item.status {
			case "done", "skipped":
				style = styles.done
			case "blocked":
				style = styles.blocked
			case "doing":
				style = styles.doing
			}
		}
		if i == m.cursor {
			style = styles.picked
		}
		lines = append(lines, style.Render(line))
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return lines
}

func (m Model) detailLines(width int) []string {
	if m.overlay != overlayNone {
		return m.overlayLines(width)
	}
	selected, ok := m.selectedRow()
	if !ok {
		return []string{"Nothing selected."}
	}
	styles := newStyles(m.noColor)
	var lines []string
	add := func(format string, args ...any) {
		for _, line := range wrapLine(fmt.Sprintf(format, args...), max(1, width)) {
			lines = append(lines, line)
		}
	}
	addHeading := func(text string) {
		lines = append(lines, styles.title.Render(text))
	}
	addBlank := func() {
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
	}
	switch selected.kind {
	case rowSection:
		addHeading(selected.label)
		add("")
		add("This section lists plans in Queue order.")
		add("")
		add("Enter expands or collapses the section. Select a plan to see its Goal and phases.")
	case rowPlan:
		plan := selected.plan
		addHeading(fmt.Sprintf("%s %s", plan.ID, plan.Title))
		add("Status: %s", plan.Status)
		add("Path: %s", plan.Path)
		add("Progress: %d/%d units done", plan.done(), plan.total())
		add("Phases: %d", len(plan.Doc.Phases))
		addBlank()
		addHeading("Goal")
		add("%s", goalText(plan.Doc))
		addBlank()
		add("Enter expands the phases and units. Select a unit for Do, Verify, Depends and Handoff.")
	case rowPhase:
		addHeading(fmt.Sprintf("%s · %s", selected.plan.ID, selected.label))
		add("Plan: %s %s", selected.plan.ID, selected.plan.Title)
		add("Units: %d", len(selected.phase.Units))
		addBlank()
		for _, unit := range selected.phase.Units {
			add("[%s] %-12s %s (%s)", unit.Status, unit.ID, unit.Title, statusWord(unit.Status))
		}
	case rowUnit:
		lines = m.unitCardLines(selected, width)
		section := m.activitySection(width)
		if len(section.lines) == 0 {
			break
		}
		addBlank()
		follow := "paused: new events do not move the view"
		if m.followActivity {
			follow = "following the newest event"
		}
		addHeading(fmt.Sprintf("Activity (%d) · %s", len(m.activityEntries(selected.unit.ID)), follow))
		add("Enter expands an entry · [ ] page a large result · / search · G newest · d diff · v verify")
		lines = append(lines, section.lines...)
	}
	if len(lines) == 0 {
		return []string{"Nothing selected."}
	}
	return lines
}

// unitCardLines renders the plan, Goal and contract of the selected unit.
func (m Model) unitCardLines(selected row, width int) []string {
	styles := newStyles(m.noColor)
	var lines []string
	add := func(format string, args ...any) {
		for _, line := range wrapLine(fmt.Sprintf(format, args...), max(1, width)) {
			lines = append(lines, line)
		}
	}
	addHeading := func(text string) {
		lines = append(lines, styles.title.Render(text))
	}
	addBlank := func() {
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
	}
	unit := selected.unit
	addHeading(fmt.Sprintf("%s %s", unit.ID, unit.Title))
	add("Status: %s (%s)", statusWord(unit.Status), unit.Status)
	add("Plan: %s %s", selected.plan.ID, selected.plan.Title)
	addBlank()
	addHeading("Goal")
	add("%s", goalText(selected.plan.Doc))
	addBlank()
	addHeading("Do")
	addField(&lines, unit, "Do", width)
	addBlank()
	addHeading("Verify")
	addField(&lines, unit, "Verify", width)
	addBlank()
	addHeading("Depends")
	addField(&lines, unit, "Depends", width)
	addBlank()
	addHeading("Handoff")
	addField(&lines, unit, "Handoff", width)
	return lines
}

func (m Model) overlayLines(width int) []string {
	styles := newStyles(m.noColor)
	var lines []string
	title := "Diff"
	if m.overlay == overlayVerify {
		title = "Verify"
	}
	hint := " · Esc closes · PgUp/PgDn scroll"
	if m.overlay == overlayVerify {
		hint += " · [ ] page the log"
	}
	lines = append(lines, styles.title.Render(title+hint))
	lines = append(lines, strings.Repeat("─", max(1, width)))
	switch m.overlay {
	case overlayDiff:
		if m.diffErr != "" {
			lines = append(lines, styles.blocked.Render("cannot read git diff: "+m.diffErr))
			break
		}
		if m.diffText == "" {
			lines = append(lines, "Working tree matches HEAD; no tracked change.")
			break
		}
		lines = append(lines, strings.Split(m.diffText, "\n")...)
	case overlayVerify:
		if m.verifyErr != "" {
			lines = append(lines, styles.blocked.Render("cannot read verify log: "+m.verifyErr))
			break
		}
		if m.verifyText == "" {
			lines = append(lines, "No verify output recorded for this unit.")
			break
		}
		lines = append(lines, strings.Split(m.verifyText, "\n")...)
	}
	return lines
}

func addField(lines *[]string, unit *format.Unit, name string, width int) {
	field := unit.Field(name)
	if field == nil {
		*lines = append(*lines, "(not set)")
		return
	}
	if strings.TrimSpace(field.Value) != "" {
		for _, line := range wrapLine(field.Value, max(1, width-2)) {
			*lines = append(*lines, "  "+line)
		}
	}
	if len(field.Items) == 0 && strings.TrimSpace(field.Value) == "" {
		*lines = append(*lines, "(empty)")
		return
	}
	for _, item := range field.Items {
		wrapped := wrapLine(item.Text, max(1, width-4))
		if len(wrapped) == 0 {
			*lines = append(*lines, "  -")
			continue
		}
		*lines = append(*lines, "  - "+wrapped[0])
		for _, rest := range wrapped[1:] {
			*lines = append(*lines, "    "+rest)
		}
	}
}

func (m Model) pane(title string, content []string, _ int, width, height int, focused bool) []string {
	styles := newStyles(m.noColor)
	marker := "  "
	if focused {
		marker = "> "
	}
	titleLine := padLine(styles.title.Render(truncate(marker+title, width)), width)
	lines := []string{titleLine}
	for _, line := range content {
		if len(lines) >= height {
			break
		}
		lines = append(lines, padLine(truncate(line, width), width))
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

func (m Model) bodyHeight() int {
	height := m.height
	if height <= 0 {
		height = 24
	}
	return max(1, height-5)
}

func (m Model) singlePanel() bool {
	width := m.width
	if width <= 0 {
		width = 80
	}
	return width < 100
}

func (m Model) visibleDetailLines(width int) []string {
	lines := m.detailLines(width)
	offset := m.detailTop
	if offset < 0 {
		offset = 0
	}
	if offset > len(lines) {
		offset = len(lines)
	}
	return lines[offset:]
}

func (m Model) clampDetailTop() Model {
	lines := m.detailLines(m.detailWidth())
	maxTop := len(lines) - m.bodyHeight()
	if maxTop < 0 {
		maxTop = 0
	}
	if m.detailTop > maxTop {
		m.detailTop = maxTop
	}
	if m.detailTop < 0 {
		m.detailTop = 0
	}
	return m
}

func (m Model) detailWidth() int {
	width := m.width
	if width <= 0 {
		width = 80
	}
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

func (m Model) statusLine() string {
	parts := []string{strings.ToUpper(m.state.String())}
	if m.state == StateReady {
		parts = append(parts, fmt.Sprintf("queue:%d frozen:%d", len(m.snapshot.Queue), len(m.snapshot.Frozen)))
		if selected, ok := m.selectedRow(); ok {
			status := selected.status
			if selected.kind == rowUnit {
				status = statusWord(selected.unit.Status)
			}
			parts = append(parts, fmt.Sprintf("selected:%s [%s]", selected.key, status))
		}
		if strings.TrimSpace(m.query) != "" {
			parts = append(parts, fmt.Sprintf("search:%q matches:%d", strings.TrimSpace(m.query), m.matches))
		}
	}
	if m.state == StateReady {
		focusName := "tree"
		if m.focus == focusDetails {
			focusName = "details"
		}
		parts = append(parts, "focus:"+focusName)
		if strings.TrimSpace(m.activityQuery) != "" {
			parts = append(parts, fmt.Sprintf("activity-search:%q matches:%d", strings.TrimSpace(m.activityQuery), m.activityMatches))
		}
		if unit := m.selectedUnit(); unit != "" {
			if entries := m.activityEntries(unit); len(entries) > 0 {
				follow := "follow"
				if !m.followActivity {
					follow = "paused"
				}
				parts = append(parts, fmt.Sprintf("activity:%d/%d %s", m.activityCursor+1, len(entries), follow))
			}
		}
	}
	if m.overlay != overlayNone {
		name := "diff"
		if m.overlay == overlayVerify {
			name = "verify"
		}
		parts = append(parts, "overlay:"+name)
	}
	if m.notice != "" {
		parts = append(parts, m.notice)
	}
	return strings.Join(parts, " · ")
}

func keysLine() string {
	return "↑↓ move · Enter expand · Tab panel · / search · d diff · v verify · f follow · ? help · q quit"
}

func goalText(plan *format.Plan) string {
	section := plan.Section("Goal")
	if section == nil {
		return "(no Goal section)"
	}
	var lines []string
	for i := section.Line; i < section.End && i < len(plan.Raw); i++ {
		lines = append(lines, strings.TrimRight(plan.Raw[i], "\r\n"))
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return "(empty Goal)"
	}
	return strings.Join(lines, " ")
}

func wrapLine(text string, width int) []string {
	if width < 1 {
		width = 1
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	paragraphs := strings.Split(text, "\n")
	var out []string
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimRight(paragraph, " \t")
		if paragraph == "" {
			out = append(out, "")
			continue
		}
		runes := []rune(paragraph)
		for len(runes) > 0 {
			used := 0
			cut := len(runes)
			lastSpace := -1
			for i, r := range runes {
				w := lipgloss.Width(string(r))
				if used+w > width {
					cut = i
					if cut == 0 {
						cut = 1
					}
					if lastSpace > 0 {
						cut = lastSpace
					}
					break
				}
				used += w
				if r == ' ' || r == '\t' {
					lastSpace = i
				}
			}
			if cut >= len(runes) {
				out = append(out, string(runes))
				break
			}
			line := strings.TrimRight(string(runes[:cut]), " \t")
			if line != "" {
				out = append(out, line)
			}
			rest := cut
			for rest < len(runes) && (runes[rest] == ' ' || runes[rest] == '\t') {
				rest++
			}
			runes = runes[rest:]
		}
	}
	return out
}

func truncate(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	runes := []rune(text)
	used := 0
	for i, r := range runes {
		w := lipgloss.Width(string(r))
		if used+w > width-1 {
			return string(runes[:i]) + "…"
		}
		used += w
	}
	return text
}

func padLine(text string, width int) string {
	text = truncate(text, width)
	pad := width - lipgloss.Width(text)
	if pad <= 0 {
		return text
	}
	return text + strings.Repeat(" ", pad)
}
