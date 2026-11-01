// Package tui renders the terminal plan workspace for docflow.
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/runner"
)

// State is the load state of the workspace.
type State int

const (
	StateLoading State = iota
	StateReady
	StateEmpty
	StateError
)

func (s State) String() string {
	switch s {
	case StateLoading:
		return "loading"
	case StateReady:
		return "ready"
	case StateEmpty:
		return "empty"
	case StateError:
		return "error"
	default:
		return "unknown"
	}
}

// PlanView is one parsed plan together with its Queue placement.
type PlanView struct {
	Doc    *format.Plan
	ID     string
	Title  string
	Status string
	Path   string
}

// Snapshot is the on-disk plan state the workspace renders.
type Snapshot struct {
	Root    string
	Queue   []*PlanView
	Frozen  []*PlanView
	Backlog int
}

// Loader reads the current plan state. It is a field so tests can supply
// fixtures without touching disk.
type Loader func() (Snapshot, error)

type rowKind int

const (
	rowSection rowKind = iota
	rowPlan
	rowPhase
	rowUnit
)

type row struct {
	kind   rowKind
	key    string
	depth  int
	label  string
	plan   *PlanView
	phase  *format.Phase
	unit   *format.Unit
	status string
}

type focus int

const (
	focusTree focus = iota
	focusDetails
)

type searchTarget int

const (
	searchTree searchTarget = iota
	searchActivity
)

type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayDiff
	overlayVerify
)

// Model is the Bubble Tea model of the plan workspace.
type Model struct {
	root string
	load Loader

	state    State
	snapshot Snapshot
	errText  string

	width  int
	height int
	ready  bool

	focus      focus
	expanded   map[string]bool
	selected   string
	cursor     int
	treeScroll int
	detailTop  int

	searching    bool
	searchTarget searchTarget
	query        string
	matches      int

	help    bool
	notice  string
	noColor bool

	skillDirectory string
	controller     Controller
	newController  func(root, skillDirectory string) Controller
	controllerDone bool
	runState       runner.State
	runErr         string
	runStopped     bool
	checkpointed   bool
	pendingPrompt  *Prompt

	dialog       dialogKind
	dialogTitle  string
	dialogLines  []string
	dialogInput  string
	dialogNote   bool
	exitAfterRun bool

	blockedUnit    string
	blockedReason  string
	blockedPending bool

	lifecycle func(root string, args ...string) cycle.Result
	lockCheck func(root string) (int, bool, error)
	lockPID   int
	lockHeld  bool
	lockErr   error

	history          *ActivityHistory
	activityCursor   int
	followActivity   bool
	activityQuery    string
	activityMatches  int
	expandedActivity map[string]bool
	pages            map[string]*activityPage
	pageOrder        []string

	overlay     overlayKind
	diffText    string
	diffErr     string
	verifyText  string
	verifyErr   string
	verifyLog   string
	verifyRef   agentio.ActivityRef
	verifySkip  int
	verifyTotal int
}

// New builds the workspace model for the project at root.
func New(root string) Model {
	return newModel(root, func() (Snapshot, error) { return Load(root) }, colorDisabled())
}

func colorDisabled() bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return true
	}
	return os.Getenv("TERM") == "dumb"
}

func newModel(root string, load Loader, noColor bool) Model {
	return Model{
		root:             root,
		load:             load,
		state:            StateLoading,
		expanded:         map[string]bool{},
		noColor:          noColor,
		history:          NewActivityHistory(activityHistoryLimit),
		followActivity:   true,
		expandedActivity: map[string]bool{},
		pages:            map[string]*activityPage{},
		newController:    func(root, skills string) Controller { return NewHost(root, skills) },
		lifecycle:        cycle.Self,
		lockCheck:        runner.CheckoutLockOwner,
	}
}

type snapshotMsg struct {
	snapshot Snapshot
	err      error
}

// reloadingText is the placeholder the r key puts in the status line until the
// load it started completes.
const reloadingText = "reloading"

// Init starts the first load.
func (m Model) Init() tea.Cmd { return loadCmd(m.load) }

func loadCmd(load Loader) tea.Cmd {
	return func() tea.Msg {
		snapshot, err := load()
		return snapshotMsg{snapshot: snapshot, err: err}
	}
}

// Update handles terminal size, loaded state, and key presses.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.syncCursor()
		if m.focus == focusDetails {
			return m.syncActivityScroll(), nil
		}
		return m, nil
	case snapshotMsg:
		return m.applySnapshot(msg), nil
	case ActivityMsg:
		m = m.applyEvent(msg.Event)
		if eventReloadsPlan(msg.Event.Type) {
			return m, loadCmd(m.load)
		}
		return m, nil
	case hostPollMsg:
		return m.pollController()
	case lifecycleDoneMsg:
		return m.applyLifecycle(msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// ApplyEvent records one runner observation and follows it when the reader is
// at the end of the history. It is the seam the interactive host uses.
func (m Model) ApplyEvent(event runner.Event) Model {
	return m.applyEvent(event)
}

func (m Model) applyEvent(event runner.Event) Model {
	if m.history == nil {
		m.history = NewActivityHistory(activityHistoryLimit)
	}
	_, recorded := m.history.Add(event)
	if event.Unit == m.selectedUnit() {
		m.refreshLivePage(event)
		m.syncLivePages(event.Unit)
	}
	if !recorded {
		return m
	}
	if event.Unit == m.selectedUnit() && m.followActivity {
		m.syncActivityCursor()
		if m.focus == focusDetails {
			return m.syncActivityScroll()
		}
	}
	return m
}

// selectedUnit returns the id of the selected unit row, or an empty string.
func (m Model) selectedUnit() string {
	selected, ok := m.selectedRow()
	if !ok || selected.kind != rowUnit {
		return ""
	}
	return selected.unit.ID
}

func (m Model) applySnapshot(msg snapshotMsg) Model {
	if msg.err != nil {
		m.state = StateError
		m.errText = msg.err.Error()
		m.notice = ""
		if m.blockedPending {
			// The block decision waits for this snapshot: name both the read
			// error and the block that is still on disk. The pending flag stays
			// set so a successful retry still resolves the outcome.
			m.notice = fmt.Sprintf("cannot reload the plan after the block: %s; the plan is still blocked — press r to try again", msg.err)
		}
		m.syncCursor()
		return m
	}
	m.snapshot = msg.snapshot
	if m.snapshot.Root == "" {
		m.snapshot.Root = m.root
	}
	m.errText = ""
	// A reload keeps the outcome the status line was showing, such as the
	// archive path of a close or why a run stopped: the tree changing does not
	// undo that. Only the load's own placeholder is cleared, and only while
	// that load is still the current action (StateLoading). A new action
	// replaces the notice itself.
	if m.state == StateLoading && m.notice == reloadingText {
		m.notice = ""
	}
	if len(m.snapshot.Queue) == 0 && len(m.snapshot.Frozen) == 0 {
		m.state = StateEmpty
	} else {
		m.state = StateReady
	}
	m.refreshLock()
	m.recountMatches()
	m.syncCursor()
	if m.blockedPending {
		// The snapshot loaded before the run stopped cannot see the `[!]` that
		// `docflow block` writes while the runner is still stopping. This
		// reload is the first one that can.
		m.blockedPending = false
		m = m.resolveBlocked()
	}
	return m
}

// Load reads docs/docflow.md and every plan it names. A missing or malformed
// plan fails the whole snapshot so the UI can show one actionable error.
func Load(root string) (Snapshot, error) {
	if err := format.CheckVersion(root); err != nil {
		return Snapshot{}, err
	}
	doc, err := format.LoadDocflow(filepath.Join(root, filepath.FromSlash(format.DocflowFilePath)))
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{Root: root, Backlog: len(doc.Backlog)}
	loadPlans := func(items []format.Item) ([]*PlanView, error) {
		plans := make([]*PlanView, 0, len(items))
		for _, item := range items {
			id := strings.TrimSpace(item.Text)
			if id == "" {
				continue
			}
			path, err := findPlanPath(root, id)
			if err != nil {
				return nil, err
			}
			plan, err := format.LoadPlan(filepath.Join(root, filepath.FromSlash(path)))
			if err != nil {
				return nil, err
			}
			plans = append(plans, newPlanView(plan, path))
		}
		return plans, nil
	}
	if snapshot.Queue, err = loadPlans(doc.Queue); err != nil {
		return Snapshot{}, err
	}
	if snapshot.Frozen, err = loadPlans(doc.Frozen); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func findPlanPath(root, id string) (string, error) {
	plans := filepath.Join(root, "docs", "plans")
	found, err := filepath.Glob(filepath.Join(plans, id+"-*.md"))
	if err != nil {
		return "", err
	}
	plain, err := filepath.Glob(filepath.Join(plans, id+".md"))
	if err != nil {
		return "", err
	}
	found = append(found, plain...)
	sort.Strings(found)
	if len(found) == 0 {
		return "", fmt.Errorf("%s: no plan in docs/plans/", id)
	}
	rel, err := filepath.Rel(root, found[0])
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

func newPlanView(plan *format.Plan, path string) *PlanView {
	view := &PlanView{
		Doc:    plan,
		ID:     plan.Meta["id"],
		Title:  plan.Meta["title"],
		Status: plan.Meta["status"],
		Path:   path,
	}
	if view.ID == "" {
		view.ID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if view.Status == "" {
		view.Status = "unknown"
	}
	return view
}

func (p *PlanView) path() string { return p.Path }

func (p *PlanView) done() int {
	count := 0
	for _, unit := range p.Doc.Units {
		if unit.Status == "x" || unit.Status == "-" {
			count++
		}
	}
	return count
}

func (p *PlanView) total() int { return len(p.Doc.Units) }

func (p *PlanView) unit(id string) *format.Unit { return p.Doc.Unit(id) }

func (m Model) selectedRow() (row, bool) {
	rows := m.rows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return row{}, false
	}
	return rows[m.cursor], true
}

func (m Model) rows() []row {
	query := strings.ToLower(strings.TrimSpace(m.query))
	var rows []row
	addSection := func(title, key string, plans []*PlanView) {
		sectionRows := []row{}
		for _, plan := range plans {
			children := m.planRows(plan, query)
			if query != "" && len(children) == 0 {
				continue
			}
			sectionRows = append(sectionRows, row{
				kind: rowPlan, key: planKey(plan), depth: 1, plan: plan,
				label: planLabel(plan), status: plan.Status,
			})
			if query != "" || m.isExpanded(planKey(plan)) {
				sectionRows = append(sectionRows, children...)
			}
		}
		if query != "" && len(sectionRows) == 0 {
			return
		}
		rows = append(rows, row{
			kind: rowSection, key: "section:" + key, depth: 0,
			label:  fmt.Sprintf("%s (%d)", title, len(plans)),
			status: fmt.Sprintf("%d plans", len(plans)),
		})
		if query != "" || m.isExpanded("section:"+key) {
			rows = append(rows, sectionRows...)
		}
	}
	addSection("Queue", "queue", m.snapshot.Queue)
	addSection("Frozen", "frozen", m.snapshot.Frozen)
	return rows
}

func (m Model) planRows(plan *PlanView, query string) []row {
	var rows []row
	planMatch := query == "" || matchesPlan(query, plan)
	seen := map[string]bool{}
	for _, phase := range plan.Doc.Phases {
		phaseMatch := planMatch || matches(query, fmt.Sprintf("Phase %d %s", phase.Number, phase.Title))
		units := []row{}
		for _, unit := range phase.Units {
			seen[unit.ID] = true
			if query == "" || planMatch || phaseMatch || matchesUnit(query, unit) {
				units = append(units, unitRow(plan, unit))
			}
		}
		if query != "" && !phaseMatch && len(units) == 0 {
			continue
		}
		key := phaseKey(plan, phase)
		rows = append(rows, row{
			kind: rowPhase, key: key, depth: 2, plan: plan, phase: phase,
			label: phaseLabel(phase), status: fmt.Sprintf("%d units", len(phase.Units)),
		})
		if query != "" || m.isExpanded(key) {
			rows = append(rows, units...)
		}
	}
	// Units outside a phase are legal in the format.
	for _, unit := range plan.Doc.Units {
		if seen[unit.ID] {
			continue
		}
		if query != "" && !planMatch && !matchesUnit(query, unit) {
			continue
		}
		rows = append(rows, unitRow(plan, unit))
	}
	return rows
}

func unitRow(plan *PlanView, unit *format.Unit) row {
	return row{
		kind: rowUnit, key: unitKey(unit), depth: 3, plan: plan, unit: unit,
		label:  unitLabel(unit),
		status: format.Statuses[unit.Status],
	}
}

func planKey(plan *PlanView) string { return "plan:" + plan.ID }
func phaseKey(plan *PlanView, phase *format.Phase) string {
	return fmt.Sprintf("phase:%s:%d", plan.ID, phase.Number)
}
func unitKey(unit *format.Unit) string { return "unit:" + unit.ID }

func planLabel(plan *PlanView) string {
	return fmt.Sprintf("%s %s [%s] %d/%d", plan.ID, plan.Title, plan.Status, plan.done(), plan.total())
}

func phaseLabel(phase *format.Phase) string {
	return fmt.Sprintf("Phase %d: %s", phase.Number, phase.Title)
}

func unitLabel(unit *format.Unit) string {
	return fmt.Sprintf("[%s] %-7s %s %s", unit.Status, statusWord(unit.Status), unit.ID, unit.Title)
}

func statusWord(status string) string {
	if word, ok := format.Statuses[status]; ok {
		return word
	}
	return "unknown"
}

func (m Model) isExpanded(key string) bool {
	value, set := m.expanded[key]
	if !set {
		return true
	}
	return value
}

func (m Model) toggle(key string) Model {
	if m.expanded == nil {
		m.expanded = map[string]bool{}
	}
	m.expanded[key] = !m.isExpanded(key)
	return m
}

func (m *Model) syncCursor() {
	rows := m.rows()
	if len(rows) == 0 {
		m.cursor = 0
		m.selected = ""
		m.treeScroll = 0
		return
	}
	if m.selected != "" {
		for i := range rows {
			if rows[i].key == m.selected {
				m.cursor = i
				return
			}
		}
	}
	if m.cursor >= len(rows) {
		m.cursor = len(rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.selected = rows[m.cursor].key
}

func (m Model) setCursor(index int) Model {
	rows := m.rows()
	if len(rows) == 0 {
		m.cursor = 0
		m.selected = ""
		m.detailTop = 0
		return m
	}
	if index < 0 {
		index = 0
	}
	if index >= len(rows) {
		index = len(rows) - 1
	}
	if rows[index].key != m.selected {
		m.detailTop = 0
		m.activityCursor = 0
		m.expandedActivity = map[string]bool{}
		m.pages = map[string]*activityPage{}
		m.pageOrder = nil
		m.activityMatches = 0
		m.overlay = overlayNone
		m.diffText, m.diffErr, m.verifyText, m.verifyErr = "", "", "", ""
		if m.followActivity {
			m.syncActivityCursor()
		}
	}
	m.cursor = index
	m.selected = rows[index].key
	return m
}

func (m Model) moveCursor(delta int) Model {
	return m.setCursor(m.cursor + delta)
}

func (m Model) parentCursor() Model {
	rows := m.rows()
	if m.cursor <= 0 || m.cursor >= len(rows) {
		return m
	}
	depth := rows[m.cursor].depth
	for i := m.cursor - 1; i >= 0; i-- {
		if rows[i].depth < depth {
			return m.setCursor(i)
		}
	}
	return m
}

func (m *Model) recountMatches() {
	m.matches = 0
	query := strings.ToLower(strings.TrimSpace(m.query))
	if query == "" {
		return
	}
	for _, plan := range append(append([]*PlanView{}, m.snapshot.Queue...), m.snapshot.Frozen...) {
		for _, phase := range plan.Doc.Phases {
			for _, unit := range phase.Units {
				if matchesUnit(query, unit) {
					m.matches++
				}
			}
		}
		for _, unit := range plan.Doc.Units {
			if unit.Phase == nil && matchesUnit(query, unit) {
				m.matches++
			}
		}
	}
}

func matchesPlan(query string, plan *PlanView) bool {
	return matches(query, plan.ID, plan.Title, plan.Status, plan.Path)
}

func matchesUnit(query string, unit *format.Unit) bool {
	parts := []string{unit.ID, unit.Title, statusWord(unit.Status)}
	for _, field := range unit.Fields {
		parts = append(parts, field.Key, field.Value)
		for _, item := range field.Items {
			parts = append(parts, item.Text)
		}
	}
	return matches(query, parts...)
}

func matches(query string, parts ...string) bool {
	query = strings.ToLower(query)
	for _, part := range parts {
		if strings.Contains(strings.ToLower(part), query) {
			return true
		}
	}
	return false
}
