package tui

import (
	"fmt"
	"path"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"bsv-docflow/internal/roster"
)

// The roster screen shows the roster workspace of the project and edits the
// conditions of it: the variants of every preset layer, the accesses with
// their switch, budget and model globs, and the role chains of the brief.
// The rows come from roster.LoadWorkspace alone and are rebuilt by reloading
// it, so the screen never patches a derived row by hand.

// rosterPhase is the load state of the roster screen.
type rosterPhase int

const (
	rosterIdle rosterPhase = iota
	rosterLoading
	rosterReady
	rosterFailed
)

// rosterPanel is one panel of the roster screen.
type rosterPanel int

const (
	panelVariants rosterPanel = iota
	panelAccesses
	panelRoles
)

// String is the stable panel name the status line and tests use.
func (p rosterPanel) String() string {
	switch p {
	case panelAccesses:
		return "accesses"
	case panelRoles:
		return "roles"
	default:
		return "variants"
	}
}

// title is the panel title of the screen.
func (p rosterPanel) title() string {
	switch p {
	case panelAccesses:
		return "Доступы"
	case panelRoles:
		return "Цепочки"
	default:
		return "Варианты"
	}
}

// budgetLadder is the order ←/→ walks, the most available level first. off
// is the last step: the switch key is the direct way to it.
var budgetLadder = []string{roster.BudgetPrefer, roster.BudgetNormal, roster.BudgetLean, roster.BudgetMinimal, roster.BudgetOff}

// rosterMsg is the answer of a roster load.
type rosterMsg struct {
	workspace *roster.Workspace
	err       error
}

// rosterModelList is the open model list of one access: the catalog models
// of the access and the cursor over them.
type rosterModelList struct {
	access string
	cursor int
	models []roster.ModelInfo
}

func loadRosterCmd(load func(rosterPath, root string) (*roster.Workspace, error), rosterPath, root string) tea.Cmd {
	return func() tea.Msg {
		workspace, err := load(rosterPath, root)
		return rosterMsg{workspace: workspace, err: err}
	}
}

// startRoster marks the roster loading and returns the command that reads
// it. It is called when the screen opens for the first time and by r.
func (m Model) startRoster() (Model, tea.Cmd) {
	if m.rosterLoad == nil {
		m.rosterLoad = roster.LoadWorkspace
	}
	m.rosterPhase = rosterLoading
	return m, loadRosterCmd(m.rosterLoad, m.rosterPath, m.root)
}

// applyRoster records a load: the rows of the screen are the loader's, and
// the cursors stay on a row that still exists.
func (m Model) applyRoster(msg rosterMsg) Model {
	if msg.err != nil {
		m.rosterPhase = rosterFailed
		m.rosterErr = msg.err.Error()
		m.roster = nil
		return m
	}
	m.roster = msg.workspace
	m.rosterErr = ""
	m.rosterPhase = rosterReady
	return m.clampRosterCursors()
}

// refreshRoster reloads the workspace synchronously after a write. The
// operations update the state alone; every derived row — the indicator, the
// budget, the allowlist, the chains — is built by the loader.
func (m Model) refreshRoster() Model {
	if m.rosterLoad == nil {
		m.rosterLoad = roster.LoadWorkspace
	}
	workspace, err := m.rosterLoad(m.rosterPath, m.root)
	return m.applyRoster(rosterMsg{workspace: workspace, err: err})
}

// rosterEdit runs one condition operation and reloads the workspace. A
// refusal leaves the file and the model as they were and is shown in the
// notice line with the reason the operation gave.
func (m Model) rosterEdit(done string, op func(*roster.Workspace) error) Model {
	if m.roster == nil {
		m.notice = "the roster is not loaded: press r"
		return m
	}
	if err := op(m.roster); err != nil {
		m.notice = err.Error()
		return m
	}
	m = m.refreshRoster()
	if m.rosterPhase == rosterFailed {
		m.notice = done + "; the reload failed: " + m.rosterErr
		return m
	}
	m.notice = done
	return m
}

func (m Model) clampRosterCursors() Model {
	if m.roster == nil {
		return m
	}
	m.variants = clampIndex(m.variants, len(m.roster.Variants))
	m.accesses = clampIndex(m.accesses, len(m.roster.Accesses))
	return m
}

func clampIndex(index, length int) int {
	if index < 0 {
		return 0
	}
	if index >= length {
		return max(0, length-1)
	}
	return index
}

// selectedVariant is the variant under the cursor.
func (m Model) selectedVariant() (roster.WorkspaceVariant, bool) {
	if m.roster == nil || m.variants < 0 || m.variants >= len(m.roster.Variants) {
		return roster.WorkspaceVariant{}, false
	}
	return m.roster.Variants[m.variants], true
}

// selectedAccess is the access under the cursor.
func (m Model) selectedAccess() (roster.WorkspaceAccess, bool) {
	if m.roster == nil || m.accesses < 0 || m.accesses >= len(m.roster.Accesses) {
		return roster.WorkspaceAccess{}, false
	}
	return m.roster.Accesses[m.accesses], true
}

// rosterAccess finds an access row by name.
func (m Model) rosterAccess(name string) (roster.WorkspaceAccess, bool) {
	if m.roster == nil {
		return roster.WorkspaceAccess{}, false
	}
	for _, row := range m.roster.Accesses {
		if row.Access == name {
			return row, true
		}
	}
	return roster.WorkspaceAccess{}, false
}

// moveRosterCursor moves the cursor of the focused panel.
func (m Model) moveRosterCursor(delta int) Model {
	switch m.rosterPanel {
	case panelAccesses:
		if m.roster != nil {
			m.accesses = clampIndex(m.accesses+delta, len(m.roster.Accesses))
		}
	default:
		if m.roster != nil {
			m.variants = clampIndex(m.variants+delta, len(m.roster.Variants))
		}
	}
	return m
}

// handleRosterKey is the keyboard of the roster screen: the panels, the
// condition keys of an access and the variant keys.
func (m Model) handleRosterKey(key string) (Model, tea.Cmd) {
	if m.rosterPhase != rosterReady || m.roster == nil {
		if key == "esc" {
			m.screen = screenProject
			m.focus = focusTree
		}
		return m, nil
	}
	if key == "n" {
		// n belongs to the screen, not to a panel: the saved variant is the
		// conditions in force, whichever panel has the cursor.
		m.dialogInput = ""
		m.variantProject = false
		return m.openVariantDialog(""), nil
	}
	switch m.rosterPanel {
	case panelVariants:
		switch key {
		case "up", "k":
			return m.moveRosterCursor(-1), nil
		case "down", "j":
			return m.moveRosterCursor(1), nil
		case "pgup":
			return m.moveRosterCursor(-max(1, m.paneContentHeight()/2)), nil
		case "pgdown":
			return m.moveRosterCursor(max(1, m.paneContentHeight()/2)), nil
		case "home", "g":
			m.variants = 0
			return m, nil
		case "end", "G":
			m.variants = max(0, len(m.roster.Variants)-1)
			return m, nil
		case "enter":
			return m.setPreset(), nil
		case "backspace":
			return m.clearPreset(), nil
		case "D":
			return m.openDeleteVariant(), nil
		case "esc":
			m.screen = screenProject
			m.focus = focusTree
			return m, nil
		}
	case panelAccesses:
		switch key {
		case "up", "k":
			return m.moveRosterCursor(-1), nil
		case "down", "j":
			return m.moveRosterCursor(1), nil
		case "pgup":
			return m.moveRosterCursor(-max(1, m.paneContentHeight()/2)), nil
		case "pgdown":
			return m.moveRosterCursor(max(1, m.paneContentHeight()/2)), nil
		case "home", "g":
			m.accesses = 0
			return m, nil
		case "end", "G":
			m.accesses = max(0, len(m.roster.Accesses)-1)
			return m, nil
		case "space":
			return m.toggleAccess(), nil
		case "left", "h":
			return m.stepBudget(-1), nil
		case "right", "l":
			return m.stepBudget(1), nil
		case "enter":
			return m.openRosterModels(), nil
		case "esc":
			m.screen = screenProject
			m.focus = focusTree
			return m, nil
		}
	case panelRoles:
		// The chains are read-only and taller than the smallest screen, so
		// the keys scroll the list instead of moving a cursor over it.
		switch key {
		case "up", "k":
			m.roleTop = max(0, m.roleTop-1)
			return m, nil
		case "down", "j":
			m.roleTop = min(m.maxRoleTop(), m.roleTop+1)
			return m, nil
		case "pgup":
			m.roleTop = max(0, m.roleTop-m.paneContentHeight())
			return m, nil
		case "pgdown":
			m.roleTop = min(m.maxRoleTop(), m.roleTop+m.paneContentHeight())
			return m, nil
		case "home", "g":
			m.roleTop = 0
			return m, nil
		case "end", "G":
			m.roleTop = m.maxRoleTop()
			return m, nil
		case "esc":
			m.screen = screenProject
			m.focus = focusTree
			return m, nil
		}
	}
	return m, nil
}

// toggleAccess switches the selected access on, or opens the dialog that
// asks until when it stays off.
func (m Model) toggleAccess() Model {
	access, ok := m.selectedAccess()
	if !ok {
		return m
	}
	if access.Off {
		m.accessTarget = access.Access
		return m.rosterEdit(access.Access+" switched on", func(w *roster.Workspace) error {
			return w.SetOn(access.Access)
		})
	}
	m.accessTarget = access.Access
	m.dialogInput = access.Until
	return m.openDialog(dialogOff, "Switch the access off", []string{
		access.Access + " is switched off until the conditions change.",
		"Date YYYY-MM-DD, empty means no end date.",
	}, []string{"Enter — switch off · Esc — cancel"})
}

// stepBudget walks the budget ladder of the selected access one level.
func (m Model) stepBudget(direction int) Model {
	access, ok := m.selectedAccess()
	if !ok {
		return m
	}
	index := 0
	for i, level := range budgetLadder {
		if level == access.Budget {
			index = i
		}
	}
	next := clampIndex(index+direction, len(budgetLadder))
	if budgetLadder[next] == access.Budget {
		return m
	}
	level := budgetLadder[next]
	return m.rosterEdit(access.Access+" budget "+level, func(w *roster.Workspace) error {
		return w.SetBudget(access.Access, level)
	})
}

// applyOffDialog answers the off dialog: an empty date means no end date, a
// date must parse before anything is written.
func (m Model) applyOffDialog() Model {
	access := m.accessTarget
	text := strings.TrimSpace(m.dialogInput)
	day := time.Time{}
	if text != "" {
		parsed, err := roster.ParseDay(text)
		if err != nil {
			m.dialogLines = []string{
				access + " is switched off until the conditions change.",
				"Date YYYY-MM-DD, empty means no end date.",
				"", err.Error(),
			}
			return m
		}
		day = parsed
	}
	m.dialog = dialogNone
	done := access + " switched off"
	if !day.IsZero() {
		done = access + " switched off until " + day.UTC().Format("2006-01-02")
	}
	return m.rosterEdit(done, func(w *roster.Workspace) error {
		return w.SetOff(access, day)
	})
}

// setPreset makes the variant under the cursor the active preset.
func (m Model) setPreset() Model {
	variant, ok := m.selectedVariant()
	if !ok {
		return m
	}
	return m.rosterEdit("preset "+variant.Name, func(w *roster.Workspace) error {
		return w.SetPreset(variant.Name)
	})
}

// clearPreset drops the active preset and keeps the other conditions.
func (m Model) clearPreset() Model {
	if m.roster.Preset == "" {
		m.notice = "no active preset"
		return m
	}
	return m.rosterEdit("preset cleared", func(w *roster.Workspace) error {
		return w.ClearPreset()
	})
}

// openDeleteVariant asks for the confirmation of a delete. A preset of
// roster.toml has no dialog: the file is hand-edited, so the refusal is a
// fact and not a question.
func (m Model) openDeleteVariant() Model {
	variant, ok := m.selectedVariant()
	if !ok {
		return m
	}
	if variant.Source == roster.SourcePolicy {
		m.notice = fmt.Sprintf("%s is defined in roster.toml, which docflow never rewrites: delete the table by hand", variant.Name)
		return m
	}
	m.variantTarget = variant.Name
	return m.openDialog(dialogVariantDelete, "Delete the variant "+variant.Name+"?", []string{
		"File: " + variant.Path,
		"A variant in force is cleared with the delete.",
	}, []string{"Enter/y — delete · Esc/n — cancel"})
}

// deleteVariant removes the remembered variant from its machine file.
func (m Model) deleteVariant() Model {
	name := m.variantTarget
	m.dialog = dialogNone
	if name == "" {
		return m
	}
	return m.rosterEdit("variant "+name+" deleted", func(w *roster.Workspace) error {
		return w.DeleteVariant(name)
	})
}

// openVariantDialog asks for the name of a new variant and its target. The
// error of the last attempt stays in the body, so the name can be fixed in
// place instead of retyped.
func (m Model) openVariantDialog(errText string) Model {
	lines := []string{
		"Saves the conditions in force as a new preset.",
		"Only budgets, the access order and model allowlists are carried:",
		"a dated off, an expiry or a denial cannot be saved.",
		"",
		"target: " + m.variantTargetText(),
	}
	if errText != "" {
		lines = append(lines, "", errText)
	}
	return m.openDialog(dialogVariant, "Save the conditions as a variant", lines,
		[]string{"Enter — save · Tab — global/project · Esc — cancel"})
}

func (m Model) variantTargetText() string {
	if m.variantProject {
		return "project (docs/roster.variants.toml)"
	}
	return "global (next to roster.toml)"
}

// saveVariant writes the remembered variant name into the chosen file.
func (m Model) saveVariant() Model {
	if m.roster == nil {
		m.dialog = dialogNone
		m.notice = "the roster is not loaded: press r"
		return m
	}
	name := strings.TrimSpace(m.dialogInput)
	if err := m.roster.SaveVariant(name, m.variantProject); err != nil {
		return m.openVariantDialog(err.Error())
	}
	m.dialog = dialogNone
	m = m.refreshRoster()
	if m.rosterPhase == rosterFailed {
		m.notice = "variant " + name + " saved; the reload failed: " + m.rosterErr
		return m
	}
	m.notice = "variant " + name + " saved"
	for i, variant := range m.roster.Variants {
		if variant.Name == name {
			m.variants = i
		}
	}
	return m
}

// openRosterModels opens the model list of the selected access.
func (m Model) openRosterModels() Model {
	access, ok := m.selectedAccess()
	if !ok {
		return m
	}
	if m.roster.Catalog == nil {
		m.notice = "the catalog is empty: no models to allow or deny"
		return m
	}
	m.overlay = overlayRosterModels
	m.rmodels = &rosterModelList{access: access.Access, models: m.roster.Catalog.ModelRows([]string{access.Access})}
	return m
}

// selectedRosterModel is the model under the overlay cursor.
func (m Model) selectedRosterModel() (roster.ModelInfo, bool) {
	if m.rmodels == nil || m.rmodels.cursor < 0 || m.rmodels.cursor >= len(m.rmodels.models) {
		return roster.ModelInfo{}, false
	}
	return m.rmodels.models[m.rmodels.cursor], true
}

// refreshRosterModels rebuilds the open model list from the reloaded
// catalog and keeps the cursor inside it.
func (m Model) refreshRosterModels() Model {
	if m.rmodels == nil || m.roster == nil || m.roster.Catalog == nil {
		return m
	}
	cursor := m.rmodels.cursor
	m.rmodels = &rosterModelList{
		access: m.rmodels.access,
		cursor: clampIndex(cursor, len(m.roster.Catalog.ModelRows([]string{m.rmodels.access}))),
		models: m.roster.Catalog.ModelRows([]string{m.rmodels.access}),
	}
	return m
}

// handleRosterModelsKey edits the allow and deny globs of one access from
// the catalog list. Every edit writes the condition state, reloads the
// workspace — the chains follow the new allowlist — and keeps the list
// open.
func (m Model) handleRosterModelsKey(key string) Model {
	switch key {
	case "up", "k":
		if m.rmodels != nil && m.rmodels.cursor > 0 {
			m.rmodels.cursor--
		}
		return m
	case "down", "j":
		if m.rmodels != nil && m.rmodels.cursor < len(m.rmodels.models)-1 {
			m.rmodels.cursor++
		}
		return m
	case "home", "g":
		if m.rmodels != nil {
			m.rmodels.cursor = 0
		}
		return m
	case "end", "G":
		if m.rmodels != nil {
			m.rmodels.cursor = max(0, len(m.rmodels.models)-1)
		}
		return m
	case "a":
		return m.editModelGlob("allow")
	case "x":
		return m.editModelGlob("deny")
	case "backspace":
		return m.editModelGlob("unset")
	case "esc", "enter":
		m.overlay = overlayNone
		m.rmodels = nil
	}
	return m
}

// editModelGlob writes one model glob into the conditions of the access:
// allow and deny add it, backspace drops it from both. The policy layer is
// never rewritten.
func (m Model) editModelGlob(kind string) Model {
	selected, ok := m.selectedRosterModel()
	if !ok || m.roster == nil {
		return m
	}
	access, glob := m.rmodels.access, selected.ID
	done := ""
	op := func(w *roster.Workspace) error {
		switch kind {
		case "allow":
			return w.Allow(access, []string{glob})
		case "deny":
			return w.Deny(access, []string{glob})
		}
		return w.UnsetGlob(access, glob)
	}
	switch kind {
	case "allow":
		done = access + ": allowed " + glob
	case "deny":
		done = access + ": denied " + glob
	default:
		done = access + ": condition cleared for " + glob
	}
	if err := op(m.roster); err != nil {
		m.notice = err.Error()
		return m
	}
	m = m.refreshRoster()
	if m.rosterPhase == rosterFailed {
		m.notice = done + "; the reload failed: " + m.rosterErr
		return m
	}
	m.notice = done
	return m.refreshRosterModels()
}

// modelGlobState says what the conditions in force do with a model id.
func modelGlobState(access roster.WorkspaceAccess, id string) string {
	for _, glob := range access.Deny {
		if matched, err := path.Match(glob, id); err == nil && matched {
			return "denied"
		}
	}
	if len(access.Allow) == 0 {
		return "unrestricted"
	}
	for _, glob := range access.Allow {
		if matched, err := path.Match(glob, id); err == nil && matched {
			return "allowed"
		}
	}
	return "not in the allowlist"
}

// rosterBody renders the roster screen: three panels from 100 columns, one
// panel with Tab below it.
func (m Model) rosterBody(width, height int) []string {
	if m.overlay == overlayRosterModels {
		title := "Модели"
		if m.rmodels != nil {
			title += " " + m.rmodels.access
		}
		return m.pane(title, m.rosterModelLines(), 0, width, height, true)
	}
	switch m.rosterPhase {
	case rosterIdle, rosterLoading:
		return m.messageLines(width, height, "Читаю ростер…", m.rosterPath)
	case rosterFailed:
		return m.messageLines(width, height, "Не читается ростер.", m.rosterErr, "r — повторить · ? помощь")
	}
	workspace := m.roster
	switch {
	case workspace.Missing:
		return m.messageLines(width, height, "Нет ростера: "+workspace.PolicyPath,
			"Политика не создана. Соберите каталог: docflow roster scan",
			"и опишите доступы по docs/design/roster.md.")
	case workspace.Legacy:
		return m.messageLines(width, height, "Ростер версии 1: "+workspace.PolicyPath,
			workspace.Problem, "Обновите: docflow roster migrate --write")
	}
	if m.singlePanel() {
		var content []string
		switch m.rosterPanel {
		case panelVariants:
			content = m.variantLines()
		case panelAccesses:
			content = m.accessLines()
		default:
			content = m.roleLines()
		}
		return m.pane(m.rosterPanel.title(), content, 0, width, height, true)
	}
	left := width / 5
	if left < 22 {
		left = 22
	}
	if left > 30 {
		left = 30
	}
	rest := width - left - 2
	center := rest / 2
	right := rest - center - 1
	variants := m.pane("Варианты", m.variantLines(), 0, left, height, m.rosterPanel == panelVariants)
	accesses := m.pane("Доступы", m.accessLines(), 0, center, height, m.rosterPanel == panelAccesses)
	roles := m.pane("Цепочки", m.roleLines(), 0, right, height, m.rosterPanel == panelRoles)
	body := make([]string, height)
	for i := 0; i < height; i++ {
		body[i] = variants[i] + "│" + accesses[i] + "│" + roles[i]
	}
	return body
}

// variantLines renders the preset layers: the active one first marked, then
// the file it comes from.
func (m Model) variantLines() []string {
	workspace := m.roster
	if len(workspace.Variants) == 0 {
		return []string{"нет вариантов"}
	}
	rows := make([][]string, 0, len(workspace.Variants))
	for i, variant := range workspace.Variants {
		marker := " "
		if i == m.variants {
			marker = ">"
		}
		active := " "
		if variant.Name == workspace.Preset {
			active = "●"
		}
		rows = append(rows, []string{fmt.Sprintf("%s%s %s", marker, active, variantName(variant))})
	}
	return windowRows(rows, m.variants, m.paneContentHeight())
}

// variantName renders a variant with its source layer.
func variantName(variant roster.WorkspaceVariant) string {
	source := "roster"
	switch variant.Source {
	case roster.SourceVariants:
		source = "глоб."
	case roster.SourceProject:
		source = "проект"
	}
	return fmt.Sprintf("%s (%s)", variant.Name, source)
}

// accessLines renders the accesses: the channels and the catalog facts on
// the first line, the switch, the budget and the review flags on the second,
// the dates and the model globs on the third when the access carries any.
func (m Model) accessLines() []string {
	workspace := m.roster
	if len(workspace.Accesses) == 0 {
		return []string{"нет доступов"}
	}
	rows := make([][]string, 0, len(workspace.Accesses))
	for i, access := range workspace.Accesses {
		marker := " "
		if i == m.accesses {
			marker = ">"
		}
		block := []string{
			fmt.Sprintf("%s %s", marker, accessHead(access)),
			"    " + accessState(access),
		}
		if detail := accessDetail(access); detail != "" {
			block = append(block, "    "+detail)
		}
		rows = append(rows, block)
	}
	return windowRows(rows, m.accesses, m.paneContentHeight())
}

// accessHead is the first line of an access: the name, its channels and
// what the catalog adds.
func accessHead(access roster.WorkspaceAccess) string {
	parts := []string{access.Access}
	// The channel is worth a column unless it repeats the name: a codex
	// access reached through codex says nothing new.
	if !(len(access.Agents) == 1 && access.Agents[0] == access.Access) {
		if len(access.Agents) > 0 {
			parts = append(parts, strings.Join(access.Agents, ","))
		}
	}
	for _, detail := range []string{access.Kind, access.Auth} {
		if detail != "" {
			parts = append(parts, detail)
		}
	}
	if access.Models > 0 {
		parts = append(parts, fmt.Sprintf("%d мод.", access.Models))
	}
	return strings.Join(parts, " · ")
}

// accessState is the second line: the effective switch, budget and review
// flags.
func accessState(access roster.WorkspaceAccess) string {
	parts := []string{"выкл"}
	if !access.Off {
		parts[0] = "вкл"
	}
	parts = append(parts, "бюджет "+access.Budget)
	if access.Check {
		parts = append(parts, "check")
	}
	if access.Trusted {
		parts = append(parts, "trusted")
	}
	return strings.Join(parts, " · ")
}

// accessDetail is the third line: the dates go before the globs, so a narrow
// column cuts a long pattern instead of a date.
func accessDetail(access roster.WorkspaceAccess) string {
	parts := make([]string, 0, 4)
	if access.Until != "" {
		parts = append(parts, "off до "+access.Until)
	}
	if access.Expires != "" {
		parts = append(parts, "до "+access.Expires)
	}
	if len(access.Allow) > 0 {
		parts = append(parts, "модели "+strings.Join(access.Allow, ","))
	}
	if len(access.Deny) > 0 {
		parts = append(parts, "запрет "+strings.Join(access.Deny, ","))
	}
	return strings.Join(parts, " · ")
}

// roleLines renders the role chains with their read budget and, under them,
// one warning line and the brief problem.
func (m Model) roleLines() []string {
	lines := m.allRoleLines()
	top := max(0, min(m.roleTop, len(lines)-m.paneContentHeight()))
	return lines[top:]
}

// maxRoleTop is the last scroll offset that still fills the chains pane.
func (m Model) maxRoleTop() int {
	return max(0, len(m.allRoleLines())-m.paneContentHeight())
}

// allRoleLines builds the chains block the pane scrolls through.
func (m Model) allRoleLines() []string {
	workspace := m.roster
	if workspace == nil {
		return nil
	}
	lines := make([]string, 0, len(workspace.Roles)*3)
	for _, role := range workspace.Roles {
		head := role.Role + " · " + role.Scope
		if role.Budget != nil && role.Budget.Target > 0 {
			head += fmt.Sprintf(" · бюджет %d", role.Budget.Target)
		}
		lines = append(lines, head, "  "+chainText(role))
	}
	if len(lines) == 0 {
		lines = append(lines, "цепочек нет")
	}
	if len(workspace.Warnings) > 0 {
		lines = append(lines, "", "⚠ "+strings.Join(workspace.Warnings, " · "))
	}
	if workspace.Problem != "" {
		lines = append(lines, "", "✗ "+workspace.Problem)
	}
	return lines
}

// chainText renders the routes of a role in use order.
func chainText(role roster.RoleBrief) string {
	if len(role.Routes) == 0 {
		return "нет маршрута"
	}
	parts := make([]string, 0, len(role.Routes))
	for _, route := range role.Routes {
		parts = append(parts, route.Access+"/"+route.ID)
	}
	return strings.Join(parts, " > ")
}

// rosterModelLines renders the open model list: the conditions in force,
// then every catalog model of the access with what they do with it.
func (m Model) rosterModelLines() []string {
	if m.rmodels == nil || m.roster == nil || m.roster.Policy == nil {
		return nil
	}
	access, _ := m.rosterAccess(m.rmodels.access)
	lines := []string{
		"allow: " + dashList(access.Allow),
		"deny:  " + dashList(access.Deny),
		"",
	}
	if len(m.rmodels.models) == 0 {
		return append(lines, "в каталоге нет моделей этого доступа")
	}
	rows := make([][]string, 0, len(m.rmodels.models))
	for i, model := range m.rmodels.models {
		marker := " "
		if i == m.rmodels.cursor {
			marker = ">"
		}
		glyph := "·"
		switch modelGlobState(access, model.ID) {
		case "allowed":
			glyph = "+"
		case "denied":
			glyph = "-"
		case "not in the allowlist":
			glyph = "x"
		}
		line := fmt.Sprintf("%s%s %s", marker, glyph, model.ID)
		if class := m.roster.Policy.Models[model.Access+"/"+model.ID]; class != "" {
			line += " · " + class
		}
		if model.Context > 0 {
			line += fmt.Sprintf(" · контекст %d", model.Context)
		}
		rows = append(rows, []string{line})
	}
	// The header is above the list. The separator takes a line only when a
	// model row still fits beside it: on the smallest screen one visible
	// model beats the blank line.
	room := m.paneContentHeight() - len(lines)
	if room > 1 {
		lines = append(lines, "")
		room--
	}
	return append(lines, windowRows(rows, m.rmodels.cursor, max(1, room))...)
}

func dashList(values []string) string {
	if len(values) == 0 {
		return "—"
	}
	return strings.Join(values, " ")
}

// windowRows keeps the row under the cursor visible: the rows below it
// never push it out of the window, and a multi-line row stays whole.
func windowRows(rows [][]string, cursor, height int) []string {
	if len(rows) == 0 {
		return nil
	}
	if height < 1 {
		height = 1
	}
	cursor = clampIndex(cursor, len(rows))
	top := cursor
	budget := height
	for top >= 0 {
		need := len(rows[top]) + 1
		if top != cursor && need > budget {
			break
		}
		budget -= need
		top--
	}
	top++
	out := make([]string, 0, height)
	for i := top; i < len(rows) && len(out) < height; i++ {
		out = append(out, rows[i]...)
		if i != len(rows)-1 && len(out) < height {
			out = append(out, "")
		}
	}
	return out
}
