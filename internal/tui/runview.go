package tui

import (
	"fmt"
	"strings"
	"time"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/lint"
)

// stageState names which stages of the unit cycle the runner has reached.
// The bar is derived from runner events only: a stage the runner has not
// reported is never guessed from a status or a timeout.
type stageState struct {
	Prompt bool
	Work   bool
	Verify bool
	Done   bool
	Commit bool
}

// unitRunView is one unit of the run screen's unit pane: the plan status
// joined with what the runner observed about the unit.
type unitRunView struct {
	ID     string
	Status string
	// Started and Ended come from the unit_start and unit_end events; the
	// pane shows a live duration while the unit is still open.
	Started    time.Time
	Ended      time.Time
	HasTimes   bool
	Seconds    float64
	HasSeconds bool
	Attempt    int
	HasAttempt bool
	Route      routeField
	HasRoute   bool
	Stages     stageState
}

// Duration reports how long the unit ran: the seconds of its unit_end, or
// the time since its start while it is still open.
func (u unitRunView) Duration(now time.Time) (time.Duration, bool) {
	if u.HasSeconds {
		return time.Duration(u.Seconds * float64(time.Second)), true
	}
	if u.HasTimes && !u.Started.IsZero() {
		end := u.Ended
		if end.IsZero() || end.Before(u.Started) {
			end = now
		}
		if end.After(u.Started) {
			return end.Sub(u.Started), true
		}
	}
	return 0, false
}

// runOverview is the run screen's model of the live run: the summary line
// above the panes and the state of every unit of the plan. Unknown values
// stay zero with their Known flag false, so the view shows them as unknown
// instead of guessing a zero.
type runOverview struct {
	Plan      string
	PlanKnown bool
	UnitCount int
	Units     []unitRunView

	CurrentUnit   string
	HasCurrent    bool
	UnitIndex     int // 1-based position of CurrentUnit; 0 unknown
	Attempt       int
	HasAttempt    bool
	AttemptsLimit int

	Started time.Time
	Ended   time.Time
	HasRun  bool

	Tokens    int64
	HasTokens bool

	Route    routeField
	HasRoute bool
}

// runPlanID names the plan of the newest run_start the history recorded.
// Run events carry no unit, so they live under the empty unit key.
func runPlanID(history *ActivityHistory) string {
	if history == nil {
		return ""
	}
	id := ""
	for _, entry := range history.Entries("") {
		if entry.Type == "run_start" {
			id = strValue(entry.Fields["plan"])
		}
	}
	return id
}

// collectRunOverview joins the plan's units with the runner observations of
// the history. It reads no terminal state, so a future view can reuse it
// unchanged. selected names the unit the reader chose when no run named a
// current unit of its own.
func collectRunOverview(history *ActivityHistory, plan *PlanView, selected string, attemptsLimit int, now time.Time) runOverview {
	overview := runOverview{AttemptsLimit: attemptsLimit, CurrentUnit: selected, HasCurrent: selected != ""}
	if plan != nil {
		overview.Plan, overview.PlanKnown, overview.UnitCount = plan.ID, true, len(plan.Doc.Units)
	}
	if history != nil {
		for _, entry := range history.Entries("") {
			switch entry.Type {
			case "run_start":
				overview.Started = entry.At
				overview.HasRun = true
			case "run_end":
				overview.Ended = entry.At
			}
		}
	}
	if plan == nil {
		return overview
	}
	var newestStart time.Time
	for _, unit := range plan.Doc.Units {
		view := unitRunView{ID: unit.ID, Status: unit.Status}
		if history != nil {
			history.mu.Lock()
			if observed := history.units[unit.ID]; observed != nil {
				view = observed.run
				view.ID, view.Status = unit.ID, unit.Status
				overview.Tokens += observed.tokens
				overview.HasTokens = overview.HasTokens || observed.hasTokens
			}
			history.mu.Unlock()
		}
		overview.Units = append(overview.Units, view)
		if view.HasTimes && (overview.CurrentUnit == "" || !view.Started.Before(newestStart)) {
			// Two units that started in the same instant keep the later
			// position of the plan: the run visits them in order.
			overview.CurrentUnit, overview.HasCurrent, newestStart = view.ID, true, view.Started
		}
	}
	for index, view := range overview.Units {
		if view.ID != overview.CurrentUnit {
			continue
		}
		overview.UnitIndex = index + 1
		overview.Attempt, overview.HasAttempt = view.Attempt, view.HasAttempt
		overview.Route, overview.HasRoute = view.Route, view.HasRoute
	}
	return overview
}

// collectUnitRun folds one unit's observations into its pane view: the
// boundaries, the newest route and attempt, and the reached stages.
func collectUnitRun(entries []ActivityEntry, view *unitRunView) {
	for _, entry := range entries {
		switch entry.Type {
		case "unit_start":
			if view.Started.IsZero() {
				view.Started = entry.At
				view.HasTimes = true
			}
			view.Stages.Prompt = true
			if !view.HasRoute {
				view.Route, view.HasRoute = routeOf(entry.Fields["route"]), routePresent(entry.Fields["route"])
			}
		case "attempt":
			view.Ended = time.Time{}
			view.HasSeconds = false
			view.Stages.Verify, view.Stages.Done, view.Stages.Commit = false, false, false
			view.Stages.Work = true
			view.Route, view.HasRoute = routeOf(entry.Fields["route"]), routePresent(entry.Fields["route"])
			if n, ok := intValue(entry.Fields["n"]); ok && n > 0 {
				view.Attempt, view.HasAttempt = n, true
			}
		case "route_switch":
			view.Route, view.HasRoute = routeOf(entry.Fields["to"]), routePresent(entry.Fields["to"])
		case "verify":
			view.Stages.Verify = true
			if code, ok := intValue(entry.Fields["exit_code"]); ok && code == 0 {
				view.Stages.Done = true
			}
		case "commit":
			view.Stages.Commit = true
		case "unit_end":
			view.Ended = entry.At
			if seconds, ok := numberValue(entry.Fields["seconds"]); ok {
				view.Seconds, view.HasSeconds = seconds, true
			}
			if strValue(entry.Fields["status"]) == "done" {
				view.Stages.Done = true
			}
		}
	}
}

func routePresent(value any) bool {
	route := routeOf(value)
	return route.Agent != "" || route.Model != "" || route.Effort != ""
}

// usageTokens is the token count one usage observation contributes: its own
// total, or the sum of the counters it does report when the stream sends no
// total. A usage without any number was never counted as known.
func usageTokens(usage *agentio.ActivityUsage) int64 {
	if usage.Total > 0 {
		return int64(usage.Total)
	}
	return int64(usage.Input) + int64(usage.Output) + int64(usage.CacheRead) +
		int64(usage.CacheWrite) + int64(usage.Reasoning)
}

// runElapsed is how long the run has been live: to its end when it ended,
// to now while it is open.
func (o runOverview) runElapsed(now time.Time) (time.Duration, bool) {
	if !o.HasRun || o.Started.IsZero() {
		return 0, false
	}
	end := o.Ended
	if end.IsZero() || end.Before(o.Started) {
		end = now
	}
	if !end.After(o.Started) {
		return 0, true
	}
	return end.Sub(o.Started), true
}

// formatElapsed renders a duration for the summary line: mm:ss under an
// hour, h:mm:ss beyond it.
func formatElapsed(d time.Duration) string {
	total := int64(d / time.Second)
	hours, minutes, seconds := total/3600, (total%3600)/60, total%60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}

// formatTokens renders the token sum compactly: a plain number below ten
// thousand, k and M beyond it.
func formatTokens(tokens int64) string {
	switch {
	case tokens >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(tokens)/1_000_000)
	case tokens >= 10_000:
		return fmt.Sprintf("%dk", tokens/1000)
	default:
		return fmt.Sprintf("%d", tokens)
	}
}

// formatUnitDuration renders a duration for the unit pane: seconds with one
// decimal below a minute, m:ss beyond it.
func formatUnitDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return formatElapsed(d)
}

// runOverviewNow is the overview of the current workspace state. Rendering
// derives every line of the run screen from it.
func (m Model) runOverviewNow() runOverview {
	now := m.nowTime()
	var plan *PlanView
	if id := runPlanID(m.history); id != "" {
		for _, candidate := range append(append([]*PlanView{}, m.snapshot.Queue...), m.snapshot.Frozen...) {
			if candidate.ID == id {
				plan = candidate
				break
			}
		}
	}
	if plan == nil {
		if selected, ok := m.selectedRow(); ok {
			plan = selected.plan
		}
	}
	result := collectRunOverview(m.history, plan, m.selectedUnit(), m.runLimits.Attempts, now)
	if plan != nil {
		for i := range result.Units {
			if !result.Units[i].HasRoute && lint.ExecutorMode(plan.Doc) {
				u := plan.Doc.Units[i]
				if routes, err := agents.UnitRoleRoutes(plan.Doc, u); err == nil && len(routes) > 0 {
					r := routes[0]
					result.Units[i].Route = routeField{Agent: r.Agent, Model: r.Model, Effort: r.Effort}
					result.Units[i].HasRoute = true
				}
			}
		}
	}
	return result
}

// nowTime is the workspace clock; tests pin it for deterministic durations.
func (m Model) nowTime() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// runSummaryLine is the line above the panes: the plan, the run time, the
// unit position, the attempt, the token sum of the usage observations and
// the current route. Every unknown piece is a question mark, never a zero
// that would read as a measured value.
func (m Model) runSummaryLine() string {
	overview := m.runOverviewNow()
	plan := "?"
	if overview.PlanKnown {
		plan = overview.Plan
	}
	elapsed := "?"
	if d, ok := overview.runElapsed(m.nowTime()); ok {
		elapsed = formatElapsed(d)
	}
	unit := "юнит ?"
	switch {
	case overview.UnitIndex > 0 && overview.UnitCount > 0:
		unit = fmt.Sprintf("юнит %d/%d", overview.UnitIndex, overview.UnitCount)
	case overview.UnitCount > 0:
		unit = fmt.Sprintf("юнит ?/%d", overview.UnitCount)
	}
	attemptLimit := overview.AttemptsLimit
	if attemptLimit <= 0 {
		attemptLimit = 1
	}
	attempt := fmt.Sprintf("попытка ?/%d", attemptLimit)
	if overview.HasAttempt {
		attempt = fmt.Sprintf("попытка %d/%d", overview.Attempt, attemptLimit)
	}
	tokens := "токены ?"
	if overview.HasTokens {
		tokens = "токены " + formatTokens(overview.Tokens)
	}
	route := "?"
	if overview.HasRoute {
		route = overview.Route.String()
	}
	return strings.Join([]string{plan, elapsed, unit, attempt, tokens, route}, " · ")
}

// unitStatusGlyph is the run pane's status of one unit: the plan status
// joined with the run — the current unit is in progress even before the
// reloaded plan says so.
func unitStatusGlyph(view unitRunView, current bool) string {
	if current && view.Ended.IsZero() {
		return "▶"
	}
	switch view.Status {
	case "x", "-":
		return "✓"
	case "!":
		return "✗"
	case "~":
		return "▶"
	}
	return "○"
}

// runUnitsLines renders the left pane: the units of the run plan with
// status, duration and route, and the stage bar of the current unit pinned
// under them. The window keeps the selected unit visible.
func (m Model) runUnitsLines(width, height int) []string {
	styles := newStyles(m.noColor)
	overview := m.runOverviewNow()
	selectedUnit := m.selectedUnit()
	if len(overview.Units) == 0 {
		hint := []string{"The units of the plan appear here with a run.", "Select a unit on the Проект screen with 1, then press 3."}
		for len(hint) < height {
			hint = append(hint, "")
		}
		return hint[:height]
	}
	bar := wrapLine(m.stageBarLine(overview), max(1, width))
	if len(bar) > height {
		bar = bar[:height]
	}
	listHeight := height - len(bar) - 1
	if listHeight < 0 {
		listHeight = 0
	}
	selected := 0
	for index, view := range overview.Units {
		if view.ID == selectedUnit {
			selected = index
		}
	}
	top := selected - listHeight/2
	if top > len(overview.Units)-listHeight {
		top = len(overview.Units) - listHeight
	}
	if top < 0 {
		top = 0
	}
	lines := make([]string, 0, height)
	for index := top; index < len(overview.Units) && len(lines) < listHeight; index++ {
		view := overview.Units[index]
		marker := "  "
		if view.ID == selectedUnit {
			marker = "> "
		}
		current := overview.HasCurrent && view.ID == overview.CurrentUnit
		duration := "·"
		if d, ok := view.Duration(m.nowTime()); ok {
			duration = formatUnitDuration(d)
		}
		route := "—"
		if view.HasRoute {
			route = view.Route.String()
		}
		line := fmt.Sprintf("%s%s %-10s %-7s %s", marker, unitStatusGlyph(view, current), view.ID, duration, route)
		style := styles.unit
		switch {
		case current || view.Status == "~":
			style = styles.doing
		case view.Status == "x" || view.Status == "-" || view.Stages.Commit:
			style = styles.done
		case view.Status == "!":
			style = styles.blocked
		}
		if view.ID == selectedUnit {
			style = styles.picked
		}
		text := padLine(truncate(line, max(1, width)), width)
		if m.noColor {
			lines = append(lines, text)
		} else {
			lines = append(lines, style.Render(text))
		}
	}
	for len(lines) < listHeight {
		lines = append(lines, strings.Repeat(" ", max(1, width)))
	}
	if height > len(bar) {
		lines = append(lines, "")
	}
	for _, line := range bar {
		lines = append(lines, padLine(line, width))
	}
	return lines[:height]
}

// stageBarLine renders the stages of the current unit as the runner reached
// them: prompt · work · verify · done · commit. A filled dot names a reached
// stage, a hollow one a stage without its event.
func (m Model) stageBarLine(overview runOverview) string {
	stages := []struct {
		name    string
		reached bool
	}{
		{"prompt", false},
		{"work", false},
		{"verify", false},
		{"done", false},
		{"commit", false},
	}
	if overview.HasCurrent {
		for _, view := range overview.Units {
			if view.ID != overview.CurrentUnit {
				continue
			}
			stages[0].reached = view.Stages.Prompt
			stages[1].reached = view.Stages.Work
			stages[2].reached = view.Stages.Verify
			stages[3].reached = view.Stages.Done
			stages[4].reached = view.Stages.Commit
			break
		}
	}
	parts := make([]string, 0, len(stages))
	for _, stage := range stages {
		part := "○ " + stage.name
		if stage.reached {
			part = "● " + stage.name
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " · ")
}

// runBody renders the run screen: the summary line above the panes, the
// units of the plan on the left and the stream of the selected unit on the
// right. Below 100 columns one panel is shown and Tab switches it.
func (m Model) runBody(width, height int) []string {
	styles := newStyles(m.noColor)
	if height < 2 {
		return []string{padLine("", width)}
	}
	header := m.runHeaderLines(width, height)
	for i := range header {
		if !m.noColor {
			header[i] = styles.dim.Render(header[i])
		}
		header[i] = padLine(header[i], width)
	}
	paneHeight := height - len(header)
	if m.singlePanel() {
		if m.focus == focusTree {
			return append(header, m.pane("Юниты", m.runUnitsLines(width, paneHeight-1), 0, width, paneHeight, true)...)
		}
		return append(header, m.pane("Поток", m.visibleStreamLines(width), 0, width, paneHeight, true)...)
	}
	leftWidth := width * 2 / 5
	if leftWidth < 24 {
		leftWidth = 24
	}
	if width-leftWidth-1 < minWidth/2 {
		leftWidth = width * 3 / 5
	}
	rightWidth := width - leftWidth - 1
	left := m.pane("Юниты", m.runUnitsLines(leftWidth, paneHeight-1), 0, leftWidth, paneHeight, m.focus == focusTree)
	right := m.pane("Поток", m.visibleStreamLines(rightWidth), 0, rightWidth, paneHeight, m.focus == focusDetails)
	body := make([]string, paneHeight)
	for i := range body {
		body[i] = left[i] + "│" + right[i]
	}
	return append(header, body...)
}

func (m Model) runHeaderLines(width, height int) []string {
	lines := wrapLine(m.runSummaryLine(), max(1, width))
	return lines[:min(len(lines), max(1, height-3))]
}
