package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/roster"
	"bsv-docflow/internal/runner"
)

type modelsRowKind int

const (
	modelsRole modelsRowKind = iota
	modelsUnit
)

type modelsRow struct {
	kind   modelsRowKind
	key    string
	label  string
	role   string
	routes []agents.Route
}

// modelsScreen is the "Models and executors" editor. It keeps the saved
// configuration and a draft editor apart: only Enter in the editor validates
// and writes, Esc leaves the snapshot untouched.
type modelsScreen struct {
	plan        *PlanView
	config      *runner.RunConfiguration
	agentConfig map[string]any
	roster      map[string][]agents.Route
	rows        []modelsRow
	cursor      int
	editing     bool
	input       string
	notice      string
}

func loadModelsConfig(plan *format.Plan, args agents.RunArgs) (map[string]any, error) {
	return agents.RunConfig(plan, &args)
}

// loadModelsRoster reads the personal roster for the roles the plan uses. A
// missing policy is an empty roster, not an error: the editor still shows the
// plan assignments.
func loadModelsRoster(plan *format.Plan) (map[string][]agents.Route, error) {
	path := roster.PolicyPath("")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	policy, err := roster.LoadPolicy(path)
	if err != nil {
		return nil, err
	}
	state, err := roster.LoadState(roster.StatePath(path))
	if err != nil {
		return nil, err
	}
	catalog, err := roster.LoadCatalog("")
	if err != nil {
		return nil, err
	}
	brief, err := roster.BuildBrief(policy, state, catalog, roster.PlanRoles(plan))
	if err != nil {
		return nil, err
	}
	result := map[string][]agents.Route{}
	for _, entry := range brief.Roles {
		for _, candidate := range entry.Routes {
			result[entry.Role] = append(result[entry.Role], candidate.Route())
		}
	}
	return result, nil
}

// activeRunPlan picks the plan the current Queue run would take: the selected
// plan when it is queued, otherwise the first non-draft queued plan.
func (m Model) activeRunPlan() *PlanView {
	if plan := m.selectedPlan(); plan != nil && m.inQueue(plan.ID) && plan.Status != "draft" {
		return plan
	}
	for _, plan := range m.snapshot.Queue {
		if plan.Status != "draft" {
			return plan
		}
	}
	if len(m.snapshot.Queue) > 0 {
		return m.snapshot.Queue[0]
	}
	return m.selectedPlan()
}

// openModels loads the saved overlay and opens the assignment editor.
func (m Model) openModels() Model {
	plan := m.activeRunPlan()
	if plan == nil {
		m.notice = "select a plan to configure its executors"
		return m
	}
	config, err := runner.LoadConfiguration(m.root)
	configErr := ""
	if err != nil {
		// The editor still shows the plan assignments; only saving needs the
		// git-directory state.
		config = runner.NewRunConfiguration()
		configErr = err.Error()
	}
	loader := m.modelsConfig
	if loader == nil {
		loader = loadModelsConfig
	}
	agentConfig, err := loader(plan.Doc, agents.RunArgs{})
	if err != nil {
		m.notice = err.Error()
		return m
	}
	screen := &modelsScreen{plan: plan, config: config, agentConfig: agentConfig, notice: configErr}
	if m.modelsRoster != nil {
		screen.roster, err = m.modelsRoster(plan.Doc)
		if err != nil {
			screen.notice = "personal roster unavailable: " + err.Error()
		}
	}
	m.models = screen
	return m.refreshModels()
}

// refreshModels rebuilds the rows from the plan under the saved overlay.
func (m Model) refreshModels() Model {
	screen := m.models
	if screen == nil || screen.plan == nil {
		return m
	}
	effective, err := runner.EffectiveAssignments(screen.plan.Doc, agents.RunArgs{}, screen.agentConfig, screen.config)
	if err != nil {
		screen.notice = err.Error()
		screen.rows = nil
		return m
	}
	roles := make([]string, 0, len(effective.Roles))
	for role := range effective.Roles {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	rows := make([]modelsRow, 0, len(roles)+len(effective.Units))
	for _, role := range roles {
		rows = append(rows, modelsRow{
			kind: modelsRole, key: role, role: role,
			label:  "role " + role,
			routes: append([]agents.Route(nil), effective.Roles[role]...),
		})
	}
	for _, unit := range effective.Units {
		label := unit.Unit + " [" + unit.Status + "] " + unit.Title
		if unit.Role != "" {
			label += " · role " + unit.Role
		}
		rows = append(rows, modelsRow{
			kind: modelsUnit, key: unit.Unit, role: unit.Role,
			label:  label,
			routes: append([]agents.Route(nil), unit.Routes...),
		})
	}
	screen.rows = rows
	if screen.cursor >= len(rows) {
		screen.cursor = max(0, len(rows)-1)
	}
	return m
}

func (m Model) handleModelsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	screen := m.models
	if screen == nil {
		return m, nil
	}
	key := msg.String()
	if screen.editing {
		return m.handleModelsEditKey(msg), nil
	}
	switch key {
	case "esc", "m", "q":
		m.models = nil
		return m, nil
	case "up", "k":
		if screen.cursor > 0 {
			screen.cursor--
		}
		return m, nil
	case "down", "j":
		if screen.cursor < len(screen.rows)-1 {
			screen.cursor++
		}
		return m, nil
	case "home", "g":
		screen.cursor = 0
		return m, nil
	case "end", "G":
		screen.cursor = max(0, len(screen.rows)-1)
		return m, nil
	case "enter", "e":
		if screen.cursor < 0 || screen.cursor >= len(screen.rows) {
			return m, nil
		}
		if m.runIsActive() {
			screen.notice = "assignments cannot change while a run is active; pause or stop it first"
			return m, nil
		}
		screen.editing = true
		screen.input = routeChainTextForEdit(screen.rows[screen.cursor].routes)
		screen.notice = "Enter confirms and saves · Esc cancels without changing the snapshot · - resets to the plan"
		return m, nil
	}
	return m, nil
}

func (m Model) handleModelsEditKey(msg tea.KeyPressMsg) Model {
	screen := m.models
	key := msg.String()
	switch key {
	case "esc":
		screen.editing = false
		screen.input = ""
		screen.notice = "edit cancelled; the saved snapshot is unchanged"
		return m
	case "enter":
		return m.saveModelsEdit()
	case "backspace":
		runes := []rune(screen.input)
		if len(runes) > 0 {
			screen.input = string(runes[:len(runes)-1])
		}
		return m
	case "ctrl+u":
		screen.input = ""
		return m
	}
	if text := typedText(msg); text != "" {
		screen.input += text
	}
	return m
}

func (m Model) saveModelsEdit() Model {
	screen := m.models
	if screen.cursor < 0 || screen.cursor >= len(screen.rows) {
		return m
	}
	if m.runIsActive() {
		screen.notice = "assignments cannot change while a run is active; pause or stop it first"
		return m
	}
	row := screen.rows[screen.cursor]
	updated := cloneRunConfiguration(screen.config)
	reset := strings.TrimSpace(screen.input) == "-"
	var routes []agents.Route
	if !reset {
		parsed, err := agents.ChainRoutes(screen.input)
		if err != nil {
			screen.notice = "invalid chain: " + err.Error()
			return m
		}
		routes = parsed
	}
	if row.kind == modelsRole {
		if reset {
			delete(updated.Roles, row.role)
		} else {
			updated.Roles[row.role] = routes
		}
	} else {
		if reset {
			delete(updated.Units, row.key)
		} else {
			updated.Units[row.key] = routes
		}
	}
	saved, err := runner.SaveConfiguration(m.root, screen.plan.Doc, agents.RunArgs{}, screen.agentConfig, updated)
	if err != nil {
		screen.notice = err.Error()
		return m
	}
	screen.config = saved
	screen.editing = false
	screen.input = ""
	screen.notice = "saved " + screen.plan.ID + " assignments under the git directory; the plan and roster are unchanged"
	if warning := m.pausedAssignmentWarning(screen); warning != "" {
		screen.notice += "; " + warning
	}
	return m.refreshModels()
}

// pausedAssignmentWarning explains that Resume cannot reuse the paused session
// once the paused unit's triple changes: the runner must start a new session.
func (m Model) pausedAssignmentWarning(screen *modelsScreen) string {
	if m.runState != runner.StatePaused || !m.checkpointed {
		return ""
	}
	checkpoint, err := runner.ReadCheckpoint(m.root)
	if err != nil || checkpoint.Unit == "" {
		return ""
	}
	effective, err := runner.EffectiveAssignments(screen.plan.Doc, agents.RunArgs{}, screen.agentConfig, screen.config)
	if err != nil {
		return ""
	}
	for _, unit := range effective.Units {
		if unit.Unit != checkpoint.Unit {
			continue
		}
		if len(unit.Routes) == 0 {
			return ""
		}
		route := unit.Routes[0]
		if route.Agent == checkpoint.Agent && route.Model == checkpoint.Model && route.Effort == checkpoint.Effort {
			return ""
		}
		return fmt.Sprintf("Resume will start a new session for %s because its assignment changed", checkpoint.Unit)
	}
	return ""
}

func cloneRunConfiguration(config *runner.RunConfiguration) *runner.RunConfiguration {
	clone := runner.NewRunConfiguration()
	if config == nil {
		return clone
	}
	clone.Version = config.Version
	for role, routes := range config.Roles {
		clone.Roles[role] = append([]agents.Route(nil), routes...)
	}
	for id, routes := range config.Units {
		clone.Units[id] = append([]agents.Route(nil), routes...)
	}
	return clone
}

func routeChainTextForEdit(routes []agents.Route) string {
	parts := make([]string, 0, len(routes))
	for _, route := range routes {
		parts = append(parts, agents.RouteText(route))
	}
	return strings.Join(parts, "; ")
}

// modelsLines renders the assignment editor.
func (m Model) modelsLines(width, height int) []string {
	screen := m.models
	styles := newStyles(m.noColor)
	lines := []string{styles.title.Render("Models and executors · " + screen.plan.ID + " " + screen.plan.Title)}
	lines = append(lines, "agent | model | effort; the chain order is the fallback order")
	lines = append(lines, "")
	visible := max(1, height-8)
	offset := 0
	if screen.cursor >= visible {
		offset = screen.cursor - visible + 1
	}
	for i := offset; i < len(screen.rows) && len(lines) < height-5; i++ {
		row := screen.rows[i]
		marker := "  "
		if i == screen.cursor {
			marker = "> "
		}
		text := fmt.Sprintf("%s%-28s %s", marker, row.label, routeChainTextForEdit(row.routes))
		line := truncate(text, width)
		if i == screen.cursor {
			line = styles.picked.Render(padLine(line, width))
		}
		lines = append(lines, line)
	}
	if len(screen.rows) == 0 {
		lines = append(lines, "  no open units to configure")
	}
	lines = append(lines, "")
	if screen.editing {
		lines = append(lines, truncate("edit: "+screen.input+"▏", width))
	} else {
		lines = append(lines, "Enter edits the selected assignment · Esc closes · - resets a row to the plan")
	}
	if screen.notice != "" {
		for _, line := range wrapLine(screen.notice, max(1, width)) {
			lines = append(lines, truncate(line, width))
			if len(lines) >= height {
				break
			}
		}
	}
	if len(screen.roster) > 0 {
		roles := make([]string, 0, len(screen.roster))
		for role := range screen.roster {
			roles = append(roles, role)
		}
		sort.Strings(roles)
		if len(lines) < height {
			lines = append(lines, "personal roster:")
		}
		for _, role := range roles {
			if len(lines) >= height {
				break
			}
			lines = append(lines, truncate("  "+role+": "+routeChainTextForEdit(screen.roster[role]), width))
		}
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		if !strings.Contains(lines[i], "\x1b") {
			lines[i] = padLine(lines[i], width)
		}
	}
	return lines
}
