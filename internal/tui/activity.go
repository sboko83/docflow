package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/runner"
)

const (
	// activityHistoryLimit bounds how many observations one unit keeps in
	// memory. Older entries are dropped first; the original bytes stay in the
	// call logs and are read back through activityPageSize.
	activityHistoryLimit = 1000
	// activityMessageLimit bounds the text of one streaming message in memory.
	activityMessageLimit = 64 * 1024
	// activityPageSize is how many bytes one page of a large result reads.
	activityPageSize = 4096
	// activityPagesPerEntry bounds the paged windows the details pane holds
	// at once while the reader walks through a call log.
	activityPagesPerEntry = 8
	// activityRenderWindow bounds how many history entries one frame renders.
	// The newest and the selected entry stay inside it; the rest are named by
	// a count line and remain reachable by scrolling.
	activityRenderWindow = 200
	// diffLimit bounds the working-tree diff the UI holds in memory.
	diffLimit = 512 * 1024
	// gitTimeout bounds one git inspection invoked by the UI.
	gitTimeout = 10 * time.Second
)

// ActivityMsg carries one typed runner observation into the workspace. The
// interactive host forwards Control events through it; the model owns every
// mutation of its history.
type ActivityMsg struct {
	Event runner.Event
}

// ActivityEntry is one observation in a unit's history: an agent activity or
// a runner event such as an attempt, a model switch, a quota wait or a verify
// result.
type ActivityEntry struct {
	Seq      int64
	At       time.Time
	Type     string
	Activity agentio.Activity
	// Truncated means a streaming message exceeded activityMessageLimit; its
	// complete source remains available in the call log through Activity.Ref.
	Truncated bool
	Fields    map[string]any
	// Log is the call log the observation was written to, taken when it
	// arrived. A resumed or re-run call reuses the attempt number, so a later
	// lookup by attempt would read the newer call's bytes.
	Log string
}

// ActivityHistory is the bounded, per-unit history of one interactive run. It
// keeps only normalized observations; original payloads stay in the call logs.
type ActivityHistory struct {
	mu    sync.Mutex
	limit int
	now   func() time.Time
	seq   int64
	units map[string]*unitHistory
}

type unitHistory struct {
	run          unitRunView
	tokens       int64
	hasTokens    bool
	entries      []ActivityEntry
	openMessages map[string]int
	dropped      int
	lastSeq      int64
	lastLog      string
	logs         map[int]string
	logMoved     time.Time
}

// NewActivityHistory returns an empty history with the given per-unit limit.
func NewActivityHistory(limit int) *ActivityHistory {
	if limit < 1 {
		limit = activityHistoryLimit
	}
	return &ActivityHistory{limit: limit, now: time.Now, units: map[string]*unitHistory{}}
}

// Add records one runner event. It reports false for observations the history
// does not show, such as configuration snapshots and stream links.
func (h *ActivityHistory) Add(event runner.Event) (ActivityEntry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.now == nil {
		h.now = time.Now
	}
	if h.limit < 1 {
		h.limit = activityHistoryLimit
	}
	if h.units == nil {
		h.units = map[string]*unitHistory{}
	}
	unit := h.units[event.Unit]
	if unit == nil {
		unit = &unitHistory{logs: map[int]string{}}
		h.units[event.Unit] = unit
	}
	if event.Type == "attempt" {
		// Message ids are scoped to one agent call.
		unit.openMessages = nil
		// The attempt names the call log before CallAgent starts, while the
		// stream link only exists once the call returned.
		if log := strValue(event.Fields["log"]); log != "" {
			if n, ok := intValue(event.Fields["n"]); ok {
				unit.logs[n] = log
			}
			unit.lastLog = log
			unit.logMoved = h.now()
		}
	}
	switch event.Type {
	case "stream":
		if log := strValue(event.Fields["log"]); log != "" {
			unit.lastLog = log
			unit.logMoved = h.now()
			if attempt, ok := intValue(event.Fields["attempt"]); ok {
				unit.logs[attempt] = log
			}
		}
		return ActivityEntry{}, false
	case "activity":
		var activity agentio.Activity
		switch data := event.Data.(type) {
		case agentio.Activity:
			activity = data
		case *agentio.Activity:
			if data == nil {
				return ActivityEntry{}, false
			}
			activity = *data
		default:
			return ActivityEntry{}, false
		}
		h.seq++
		entry := ActivityEntry{Seq: h.seq, At: eventTime(event.Fields, h.now), Type: event.Type, Activity: activity}
		if activity.Kind == agentio.ActivityKindUsage && activity.Usage != nil {
			if tokens := usageTokens(activity.Usage); tokens > 0 {
				unit.tokens += tokens
				unit.hasTokens = true
			}
		}
		if activity.Attempt > 0 {
			entry.Log = unit.logs[activity.Attempt]
		} else {
			entry.Log = unit.lastLog
		}
		if entry.Activity.Seq == 0 {
			entry.Activity.Seq = h.seq
		}
		if entry.Activity.Kind == agentio.ActivityKindMessage || entry.Activity.Kind == agentio.ActivityKindReasoning {
			if mergeStreamingMessage(unit, entry) {
				unit.lastSeq = h.seq
				return entry, true
			}
		}
		unit.append(entry, h.limit)
		if entry.Activity.Delta && entry.Activity.Message != "" {
			if unit.openMessages == nil {
				unit.openMessages = map[string]int{}
			}
			unit.openMessages[entry.Activity.Message] = len(unit.entries) - 1
		}
		unit.lastSeq = entry.Seq
		return entry, true
	case "attempt", "route_switch", "quota", "verify", "commit", "state",
		"unit_start", "unit_end", "run_start", "run_end", "replan", "close",
		"recovery_conflict", "blocked":
		h.seq++
		entry := ActivityEntry{Seq: h.seq, At: eventTime(event.Fields, h.now), Type: event.Type, Fields: copyFields(event.Fields)}
		collectUnitRun([]ActivityEntry{entry}, &unit.run)
		unit.append(entry, h.limit)
		unit.lastSeq = entry.Seq
		return entry, true
	}
	return ActivityEntry{}, false
}

// mergeStreamingMessage folds deltas and their final observation into the
// open history entry for that message. The entry keeps the first delta's
// reference so the complete original stream can be paged from its call log.
func mergeStreamingMessage(unit *unitHistory, incoming ActivityEntry) bool {
	activity := incoming.Activity
	if activity.Message == "" || unit.openMessages == nil {
		return false
	}
	index, ok := unit.openMessages[activity.Message]
	if !ok || index < 0 || index >= len(unit.entries) {
		delete(unit.openMessages, activity.Message)
		return false
	}
	entry := &unit.entries[index]
	if activity.Delta {
		appendMessageText(entry, activity.Text)
		includeMessageRef(entry, activity.Ref)
		return true
	}
	// Parsers remove already-emitted text from the final observation. Append
	// only its remaining suffix, then close the block with the final state.
	appendMessageText(entry, activity.Text)
	includeMessageRef(entry, activity.Ref)
	entry.Activity.State = activity.State
	entry.Activity.Delta = false
	entry.Activity.Seq = activity.Seq
	entry.At = incoming.At
	delete(unit.openMessages, activity.Message)
	return true
}

func appendMessageText(entry *ActivityEntry, text string) {
	if text == "" {
		return
	}
	current := len(entry.Activity.Text)
	remaining := activityMessageLimit - current
	if remaining <= 0 {
		entry.Truncated = true
		return
	}
	if len(text) > remaining {
		text = text[:remaining]
		for !utf8.ValidString(text) && len(text) > 0 {
			text = text[:len(text)-1]
		}
		entry.Truncated = true
	}
	entry.Activity.Text += text
}

func includeMessageRef(entry *ActivityEntry, ref agentio.ActivityRef) {
	if entry.Activity.Ref.Stream == "" {
		entry.Activity.Ref = ref
		return
	}
	if ref.Stream == entry.Activity.Ref.Stream && ref.Offset >= entry.Activity.Ref.Offset {
		end := ref.Offset + int64(ref.Length)
		length := end - entry.Activity.Ref.Offset
		if ref.Length < 0 || length > int64(entry.Activity.Ref.Length) {
			entry.Activity.Ref.Length = int(length)
		}
	}
}

func (u *unitHistory) append(entry ActivityEntry, limit int) {
	u.entries = append(u.entries, entry)
	if len(u.entries) <= limit {
		return
	}
	drop := len(u.entries) - limit
	if drop > len(u.entries) {
		drop = len(u.entries)
	}
	u.entries = append([]ActivityEntry{}, u.entries[drop:]...)
	u.dropped += drop
	for message, index := range u.openMessages {
		if index < drop {
			delete(u.openMessages, message)
		} else {
			u.openMessages[message] = index - drop
		}
	}
}

// Entries returns a copy of the unit's history in arrival order.
func (h *ActivityHistory) Entries(unit string) []ActivityEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.units[unit]
	if u == nil {
		return nil
	}
	return append([]ActivityEntry{}, u.entries...)
}

// Dropped reports how many oldest entries of the unit left the bounded
// history.
func (h *ActivityHistory) Dropped(unit string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if u := h.units[unit]; u != nil {
		return u.dropped
	}
	return 0
}

// LastSeq is the arrival sequence of the newest observation of the unit.
func (h *ActivityHistory) LastSeq(unit string) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if u := h.units[unit]; u != nil {
		return u.lastSeq
	}
	return 0
}

// Log returns the call log of the attempt the entry belongs to. The attempt
// event names the log before the call starts, so a lookup by attempt is exact;
// an entry without an attempt uses the newest log. A numbered attempt never
// falls back to the newest log of another attempt.
func (h *ActivityHistory) Log(unit string, attempt int) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.units[unit]
	if u == nil {
		return "", logUnknown(attempt)
	}
	if log, ok := u.logs[attempt]; ok && log != "" {
		return log, nil
	}
	if attempt > 0 {
		return "", logUnknown(attempt)
	}
	if u.lastLog != "" {
		return u.lastLog, nil
	}
	return "", logUnknown(attempt)
}

// logUnknown names the attempt whose log is missing instead of letting the
// page read another call's bytes.
func logUnknown(attempt int) error {
	if attempt > 0 {
		return fmt.Errorf("call log unknown for attempt %d", attempt)
	}
	return fmt.Errorf("call log unknown")
}

func copyFields(fields map[string]any) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]any, len(fields))
	for key, value := range fields {
		out[key] = value
	}
	return out
}

func eventTime(fields map[string]any, now func() time.Time) time.Time {
	if seconds, ok := numberValue(fields["ts"]); ok {
		return time.Unix(int64(seconds), int64((seconds-float64(int64(seconds)))*1e9))
	}
	return now()
}

func strValue(value any) string {
	text, _ := value.(string)
	return text
}

func numberValue(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case jsonNumber:
		n, err := v.Float64()
		return n, err == nil
	case string:
		n, err := strconv.ParseFloat(v, 64)
		return n, err == nil
	}
	return 0, false
}

// jsonNumber is the subset of encoding/json.Number the UI needs; it keeps a
// value decoded from a journal readable without importing encoding/json here.
type jsonNumber interface{ Float64() (float64, error) }

func intValue(value any) (int, bool) {
	n, ok := numberValue(value)
	return int(n), ok
}

type routeField struct {
	Agent  string
	Model  string
	Effort string
}

func routeOf(value any) routeField {
	if table, ok := value.(map[string]string); ok {
		return routeField{Agent: table["agent"], Model: table["model"], Effort: table["effort"]}
	}
	table, _ := value.(map[string]any)
	return routeField{Agent: strValue(table["agent"]), Model: strValue(table["model"]), Effort: strValue(table["effort"])}
}

func (r routeField) String() string {
	name := r.Agent
	if name == "" {
		name = "executor unknown"
	}
	model := r.Model
	if model == "" {
		model = "model unknown"
	}
	effort := r.Effort
	if effort == "" {
		effort = "effort unknown"
	}
	return Sanitize(name + "/" + model + " " + effort)
}

// activityKindLabel names an observation without implying a success.
func activityKindLabel(activity agentio.Activity) string {
	switch activity.Kind {
	case agentio.ActivityKindMessage:
		if activity.Role != "" && activity.Role != "assistant" {
			return activity.Role
		}
		return "message"
	case agentio.ActivityKindReasoning:
		return "reasoning"
	case agentio.ActivityKindTool:
		return "tool"
	case agentio.ActivityKindUsage:
		return "usage"
	case agentio.ActivityKindStderr:
		return "stderr"
	default:
		return "unknown event"
	}
}

// activityStateLabel tells a running tool from a confirmed result. An
// unconfirmed outcome is never shown as success.
func activityStateLabel(activity agentio.Activity) string {
	if activity.Kind != agentio.ActivityKindTool {
		return ""
	}
	switch activity.State {
	case agentio.ActivityStateRunning:
		return "started"
	case agentio.ActivityStateSucceeded:
		return "done"
	case agentio.ActivityStateFailed:
		return "failed"
	case agentio.ActivityStateCancelled:
		return "cancelled"
	default:
		return "outcome unknown"
	}
}

// Sanitize neutralizes terminal control sequences in text the agent produced.
// The original bytes stay untouched in the call log; only the rendered copy is
// changed. Newlines and tabs survive as layout, everything else in C0/C1 and
// every escape sequence is removed.
func Sanitize(text string) string {
	text = strings.ToValidUTF8(text, "\uFFFD")
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == 0x1b {
			i += size
			i = skipEscape(text, i)
			continue
		}
		switch r {
		case '\r':
			if i+size < len(text) && text[i+size] == '\n' {
				i += size
				continue
			}
			b.WriteByte('\n')
			i += size
			continue
		case '\n':
			b.WriteByte('\n')
			i += size
			continue
		case '\t':
			b.WriteString("    ")
			i += size
			continue
		}
		if (r < 0x20 && r != '\n') || r == 0x7f || (r >= 0x80 && r <= 0x9f) || isBidiControl(r) {
			i += size
			continue
		}
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

func skipEscape(text string, i int) int {
	if i >= len(text) {
		return i
	}
	switch text[i] {
	case '[':
		i++
		for i < len(text) {
			c := text[i]
			i++
			if c >= 0x40 && c <= 0x7e {
				break
			}
		}
		return i
	case ']', 'P', 'X', '^', '_':
		i++
		for i < len(text) {
			if text[i] == 0x07 {
				return i + 1
			}
			if text[i] == 0x1b && i+1 < len(text) && text[i+1] == '\\' {
				return i + 2
			}
			i++
		}
		return i
	}
	_, size := utf8.DecodeRuneInString(text[i:])
	return i + size
}

func isBidiControl(r rune) bool {
	switch r {
	case 0x202a, 0x202b, 0x202c, 0x202d, 0x202e, 0x2066, 0x2067, 0x2068, 0x2069:
		return true
	}
	return false
}

// resolveLogPath maps a stream path from a version 1 event to the file the
// workspace reads. Events store paths relative to the checkout, while the
// process may run from a subdirectory; an absolute path is taken as is. A
// relative path that leaves the checkout root is rejected instead of silently
// reading an unrelated file.
func resolveLogPath(root, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("log path is empty")
	}
	cleaned := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(cleaned) {
		return cleaned, nil
	}
	resolved := filepath.Clean(filepath.Join(root, cleaned))
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", fmt.Errorf("path %q is outside the checkout root: %v", resolved, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the checkout root", resolved)
	}
	return resolved, nil
}

// activityPage is one bounded window into an original log fragment.
type activityPage struct {
	Key   string
	Unit  string
	Seq   int64
	Text  string
	Skip  int
	Total int
	// Path is the resolved location of the original bytes, not the relative
	// path the version 1 event carries.
	Path string
	// End is the byte offset just past the raw bytes this page read. It
	// decides whether the window already reached the end of a live log.
	End  int
	Live bool
	Err  string
}

// loadActivityPage reads one page of the original bytes behind an entry. It
// keeps at most activityPagesPerEntry windows, dropping the oldest. A running
// tool whose entry is still the newest observation of the unit reads the call
// log up to its current end instead of the length of the start line.
func (m *Model) loadActivityPage(entry ActivityEntry, skip int) {
	m.loadActivityPageAs(entry, skip, false)
}

// loadLiveActivityPage reads a page up to the current end of the call log even
// when the entry has just been superseded by a newer record. The event that
// supersedes it is the last chance to show the bytes written so far.
func (m *Model) loadLiveActivityPage(entry ActivityEntry, skip int) {
	m.loadActivityPageAs(entry, skip, true)
}

func (m *Model) loadActivityPageAs(entry ActivityEntry, skip int, forceLive bool) {
	if m.pages == nil {
		m.pages = map[string]*activityPage{}
	}
	if m.pageOrder == nil {
		m.pageOrder = []string{}
	}
	unit := m.selectedUnit()
	key := activityPageKey(unit, entry)
	live := forceLive || m.liveActivity(unit, entry)
	page := &activityPage{Key: key, Unit: unit, Seq: entry.Seq, Skip: skip, Live: live}
	defer func() {
		m.pages[key] = page
		m.rememberPage(key)
	}()
	log := entry.Log
	if log == "" {
		var err error
		if log, err = m.history.Log(unit, entry.Activity.Attempt); err != nil {
			page.Err = err.Error()
			return
		}
	}
	path, err := resolveLogPath(m.root, log)
	if err != nil {
		page.Err = fmt.Sprintf("cannot read %s: %v", log, err)
		return
	}
	page.Path = path
	ref := entry.Activity.Ref
	if ref.Stream == "" {
		ref.Stream = "stdout"
	}
	var (
		text    string
		total   int
		readErr error
	)
	if live {
		text, total, readErr = agentio.ReadLiveActivityPage(path, ref, skip, activityPageSize)
		if readErr == nil && total == 0 {
			readErr = fmt.Errorf("the call log is empty")
		}
	} else {
		text, total, readErr = agentio.ReadActivityPage(path, ref, skip, activityPageSize)
	}
	if readErr != nil {
		page.Err = fmt.Sprintf("cannot read %s: %v", path, readErr)
		return
	}
	page.Text = Sanitize(text)
	page.Total = total
	page.End = skip + len(text)
}

// liveActivity reports whether the entry is a running tool and the newest
// observation of its unit. Only that entry may read a call log that is still
// growing; any later record turns the page back into a frozen window.
func (m Model) liveActivity(unit string, entry ActivityEntry) bool {
	if entry.Type != "activity" || entry.Activity.Kind != agentio.ActivityKindTool || entry.Activity.State != agentio.ActivityStateRunning {
		return false
	}
	if m.history == nil {
		return false
	}
	entries := m.history.Entries(unit)
	if len(entries) == 0 {
		return false
	}
	return entries[len(entries)-1].Seq == entry.Seq
}

// syncLivePages marks every page live only while its entry is still the last
// running observation of the unit.
func (m *Model) syncLivePages(unit string) {
	for _, page := range m.pages {
		if page.Unit != unit {
			continue
		}
		entry, ok := m.entryBySeq(unit, page.Seq)
		page.Live = ok && m.liveActivity(unit, entry)
	}
}

// refreshLivePage reloads the open first page when an event of the same
// attempt arrives, so a reader watching the call sees the log grow without
// collapsing and reopening the entry. The page is refreshed once even when the
// event itself supersedes it; syncLivePages then freezes it.
func (m *Model) refreshLivePage(event runner.Event) {
	attempt, ok := eventAttempt(event)
	if !ok {
		return
	}
	for _, page := range m.pages {
		if page.Unit != event.Unit || !page.Live || page.Skip != 0 {
			continue
		}
		entry, ok := m.entryBySeq(event.Unit, page.Seq)
		if !ok || entry.Type != "activity" || entry.Activity.Kind != agentio.ActivityKindTool || entry.Activity.State != agentio.ActivityStateRunning {
			continue
		}
		if entry.Activity.Attempt != attempt || !m.entryExpanded(event.Unit, entry) {
			continue
		}
		m.loadLiveActivityPage(entry, 0)
	}
}

func (m Model) entryBySeq(unit string, seq int64) (ActivityEntry, bool) {
	for _, entry := range m.history.Entries(unit) {
		if entry.Seq == seq {
			return entry, true
		}
	}
	return ActivityEntry{}, false
}

func eventAttempt(event runner.Event) (int, bool) {
	if activity, ok := event.Data.(agentio.Activity); ok {
		return activity.Attempt, activity.Attempt != 0
	}
	if activity, ok := event.Data.(*agentio.Activity); ok && activity != nil {
		return activity.Attempt, activity.Attempt != 0
	}
	return intValue(event.Fields["attempt"])
}

func (m *Model) rememberPage(key string) {
	for i, seen := range m.pageOrder {
		if seen == key {
			m.pageOrder = append(m.pageOrder[:i], m.pageOrder[i+1:]...)
			break
		}
	}
	m.pageOrder = append(m.pageOrder, key)
	for len(m.pageOrder) > activityPagesPerEntry {
		delete(m.pages, m.pageOrder[0])
		m.pageOrder = m.pageOrder[1:]
	}
}

func activityPageKey(unit string, entry ActivityEntry) string {
	return fmt.Sprintf("%s/%d", unit, entry.Seq)
}

// ReadWorkingDiff returns the actual working-tree diff of the checkout. The
// diff comes from git, never from an agent message: a failed git call is an
// error, not an empty change set.
func ReadWorkingDiff(root string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	diff, err := gitOutput(ctx, root, "diff", "--no-color", "--no-ext-diff", "HEAD")
	if err != nil {
		return "", err
	}
	status, statusErr := gitOutput(ctx, root, "status", "--porcelain", "--untracked-files=normal")
	var b strings.Builder
	b.WriteString(diff)
	if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
		b.WriteByte('\n')
	}
	if statusErr == nil {
		var untracked []string
		for _, line := range strings.Split(status, "\n") {
			if strings.HasPrefix(line, "?? ") {
				untracked = append(untracked, strings.TrimPrefix(line, "?? "))
			}
		}
		if len(untracked) > 0 {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString("--- untracked (not part of git diff) ---\n")
			for _, path := range untracked {
				b.WriteString(path)
				b.WriteByte('\n')
			}
		}
	}
	text := b.String()
	if text == "" {
		text = "(working tree matches HEAD)\n"
	}
	if len(text) > diffLimit {
		text = text[:diffLimit] + "\n… diff truncated at 512 KiB; run git diff for the rest\n"
	}
	return text, nil
}

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), Sanitize(message))
	}
	return stdout.String(), nil
}

// renderedActivity is the activity section of a unit's details: its lines and
// the line range each entry occupies, so cursor navigation can keep the
// selected observation visible.
type renderedActivity struct {
	lines  []string
	starts []int
	ends   []int
	// base is the index in the filtered history of the first rendered entry.
	base int
}

func (m Model) activityEntries(unit string) []ActivityEntry {
	if m.history == nil || unit == "" {
		return nil
	}
	entries := m.history.Entries(unit)
	query := strings.ToLower(strings.TrimSpace(m.activityQuery))
	if query == "" {
		return entries
	}
	out := make([]ActivityEntry, 0, len(entries))
	for _, entry := range entries {
		if entryMatches(entry, query) {
			out = append(out, entry)
		}
	}
	return out
}

func (m Model) hasActivity() bool {
	return len(m.activityEntries(m.selectedUnit())) > 0
}

// syncActivityCursor puts the cursor on the newest observation when follow is
// on. The follow flag is cleared as soon as the reader moves into older
// history, so new events do not move the view under the reader.
func (m *Model) syncActivityCursor() {
	entries := m.activityEntries(m.selectedUnit())
	if len(entries) == 0 {
		m.activityCursor = 0
		return
	}
	if m.followActivity || m.activityCursor >= len(entries) {
		m.activityCursor = len(entries) - 1
	}
	if m.activityCursor < 0 {
		m.activityCursor = 0
	}
	m.activityMatches = m.countActivityMatches()
}

func (m Model) countActivityMatches() int {
	if strings.TrimSpace(m.activityQuery) == "" {
		return 0
	}
	return len(m.activityEntries(m.selectedUnit()))
}

func entryMatches(entry ActivityEntry, query string) bool {
	parts := []string{entry.Type, activityKindLabel(entry.Activity), activityStateLabel(entry.Activity), entry.Activity.Tool, entry.Activity.Command, entry.Activity.Agent, entry.Activity.Model}
	parts = append(parts, Sanitize(entry.Activity.Text), Sanitize(entry.Activity.Result), Sanitize(entry.Activity.Raw))
	for _, file := range entry.Activity.Files {
		parts = append(parts, file.Path, file.Kind)
	}
	for _, value := range entry.Fields {
		parts = append(parts, fieldText(value)...)
	}
	for _, part := range parts {
		if part != "" && strings.Contains(strings.ToLower(part), query) {
			return true
		}
	}
	return false
}

func fieldText(value any) []string {
	switch v := value.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, item := range v {
			out = append(out, fieldText(item)...)
		}
		return out
	case map[string]any:
		var out []string
		for _, item := range v {
			out = append(out, fieldText(item)...)
		}
		return out
	}
	return nil
}

func entryKey(unit string, entry ActivityEntry) string {
	return fmt.Sprintf("activity:%s:%d", unit, entry.Seq)
}

func (m Model) entryExpanded(unit string, entry ActivityEntry) bool {
	return m.expandedActivity[entryKey(unit, entry)]
}

func (m *Model) activitySection(width int) renderedActivity {
	unit := m.selectedUnit()
	entries := m.activityEntries(unit)
	if unit == "" || len(entries) == 0 {
		return renderedActivity{}
	}
	styles := newStyles(m.noColor)
	groups := toolGroups(entries)
	now := m.nowTime()
	var out renderedActivity
	add := func(text string) {
		out.lines = append(out.lines, text)
	}
	render := func(text string, style lipglossStyle) string {
		if m.noColor {
			return text
		}
		return style.Render(text)
	}
	dim := func(text string) string {
		return render(text, styles.dim)
	}
	dropped := m.history.Dropped(unit)
	if dropped > 0 {
		add(dim(fmt.Sprintf("  %d older observations left the bounded history; original logs remain on disk", dropped)))
	}
	first := 0
	if len(entries) > activityRenderWindow {
		first = m.activityCursor - activityRenderWindow/2
		if first > len(entries)-activityRenderWindow {
			first = len(entries) - activityRenderWindow
		}
		if first < 0 {
			first = 0
		}
	}
	last := min(len(entries), first+activityRenderWindow)
	out.base = first
	if first > 0 {
		add(dim(fmt.Sprintf("  … %d older observations above; scroll up to read them", first)))
	}
	// The gutter is the cursor marker, the time and a space: streamed text
	// wraps into it, so one block reads as one paragraph.
	const gutter = 12
	for index := first; index < last; index++ {
		entry := entries[index]
		start := len(out.lines)
		if pair, ok := groups[index]; ok && index != pair.head && pair.head >= first {
			// The completed half of a tool call renders with its head; when
			// the head left the window the completion falls through and
			// renders alone.
			head := pair.head - first
			out.starts = append(out.starts, out.starts[head])
			out.ends = append(out.ends, out.ends[head])
			continue
		}
		cursor := index == m.activityCursor
		if pair, ok := groups[index]; ok && pair.end == m.activityCursor {
			cursor = true
		}
		marker := "  "
		if cursor {
			marker = "> "
		}
		prefix := marker + entryTimeText(entry) + " "
		groupKey := entryGroupKey(unit, entries, index, groups)
		switch {
		case entry.Type == "activity" && entry.Activity.Kind == agentio.ActivityKindReasoning:
			// Reasoning reads in full, dimmed; t folds every block to its
			// first line.
			lines := wrapLine(Sanitize(entry.Activity.Text), max(1, width-gutter-2))
			if len(lines) == 0 {
				lines = []string{""}
			}
			if m.foldReasoning {
				lines = lines[:1]
			}
			head := prefix + "◇ " + lines[0]
			if cursor {
				add(render(head, styles.picked))
			} else {
				add(dim(head))
			}
			for _, line := range lines[1:] {
				add(dim(strings.Repeat(" ", gutter) + line))
			}
			if entry.Truncated && !m.foldReasoning {
				for _, line := range m.truncatedTailLines(unit, entry, max(1, width-4)) {
					add("    " + line)
				}
			}
		case entry.Type == "activity" && entry.Activity.Kind == agentio.ActivityKindMessage:
			// Agent messages read in full; the pages of a clipped tail open
			// with Enter as before.
			role := ""
			if entry.Activity.Role != "" && entry.Activity.Role != "assistant" {
				role = "(" + Sanitize(entry.Activity.Role) + ") "
			}
			lines := wrapLine(Sanitize(entry.Activity.Text), max(1, width-gutter-len(role)))
			if len(lines) == 0 {
				lines = []string{""}
			}
			style := styles.unit
			if cursor {
				style = styles.picked
			}
			add(render(prefix+role+lines[0], style))
			for _, line := range lines[1:] {
				add(strings.Repeat(" ", gutter) + line)
			}
			if entry.Truncated {
				for _, line := range m.truncatedTailLines(unit, entry, max(1, width-4)) {
					add("    " + line)
				}
			}
		case entry.Type == "activity" && entry.Activity.Kind == agentio.ActivityKindTool:
			// A tool call is one line: the glyph, the name, the argument,
			// the state and the duration. Enter opens the result pages of
			// the completed half through the existing log paging.
			pair := groups[index]
			var end *ActivityEntry
			if pair.end >= 0 {
				end = &entries[pair.end]
			}
			style := styles.unit
			switch toolStateOf(entry, end) {
			case agentio.ActivityStateFailed:
				style = styles.blocked
			case agentio.ActivityStateRunning:
				style = styles.doing
			case agentio.ActivityStateSucceeded:
				style = styles.done
			}
			if cursor {
				style = styles.picked
			}
			add(render(prefix+toolLineText(entry, end, now), style))
			for _, file := range toolFiles(entry, end) {
				add("            ✎ " + Sanitize(file))
			}
			if m.expandedActivity[groupKey] {
				for _, line := range m.entryBody(unit, toolLoadEntry(entries, pair), max(1, width-4)) {
					add("    " + line)
				}
			}
		default:
			head := prefix + entryHeadline(entry)
			style := styles.unit
			switch {
			case entry.Activity.State == agentio.ActivityStateFailed || entryVerifyFailed(entry):
				style = styles.blocked
			case entry.Activity.State == agentio.ActivityStateRunning:
				style = styles.doing
			case entry.Activity.State == agentio.ActivityStateSucceeded:
				style = styles.done
			case entry.Type == "verify" || entry.Type == "commit":
				style = styles.done
			}
			if cursor {
				style = styles.picked
			}
			add(render(head, style))
			if m.expandedActivity[groupKey] {
				for _, line := range m.entryBody(unit, entry, max(1, width-4)) {
					add("    " + line)
				}
			} else if preview := entryPreview(entry); preview != "" {
				add("    " + truncate(preview, max(1, width-4)))
			}
		}
		out.starts = append(out.starts, start)
		out.ends = append(out.ends, len(out.lines))
	}
	if last < len(entries) {
		add(dim(fmt.Sprintf("  … %d newer observations below; scroll down or press G to follow", len(entries)-last)))
	}
	return out
}

// toolPair links a tool call's start observation with its completion: both
// render as one line, and the completed half is where the result pages read.
type toolPair struct {
	head int
	end  int // -1 while the call is still running
}

// toolGroups pairs the running and completed halves of every tool call of
// the slice. A completion without an open start renders alone; calls that
// share an empty tool id pair in arrival order.
func toolGroups(entries []ActivityEntry) map[int]toolPair {
	groups := map[int]toolPair{}
	open := map[string]int{}
	for i, entry := range entries {
		if entry.Type != "activity" || entry.Activity.Kind != agentio.ActivityKindTool {
			continue
		}
		key := fmt.Sprintf("%d/%s", entry.Activity.Attempt, entry.Activity.ToolID)
		if entry.Activity.State == agentio.ActivityStateRunning {
			open[key] = i
			groups[i] = toolPair{head: i, end: -1}
			continue
		}
		if head, ok := open[key]; ok {
			groups[head] = toolPair{head: head, end: i}
			groups[i] = toolPair{head: head, end: i}
			delete(open, key)
		} else {
			groups[i] = toolPair{head: i, end: -1}
		}
	}
	return groups
}

// entryGroupKey is the expansion key of an entry: a tool call shares its
// head's key, whichever half the cursor sits on.
func entryGroupKey(unit string, entries []ActivityEntry, index int, groups map[int]toolPair) string {
	if pair, ok := groups[index]; ok {
		return entryKey(unit, entries[pair.head])
	}
	return entryKey(unit, entries[index])
}

// toolLoadEntry is the entry whose result the pages of a tool call read: the
// completion when it arrived, the running start while the call is open.
func toolLoadEntry(entries []ActivityEntry, pair toolPair) ActivityEntry {
	if pair.end >= 0 && pair.end < len(entries) {
		return entries[pair.end]
	}
	return entries[pair.head]
}

// toolStateOf is the state of the whole call: the completion's state once
// it arrived, the running start's state before that.
func toolStateOf(head ActivityEntry, end *ActivityEntry) agentio.ActivityState {
	if end != nil {
		return end.Activity.State
	}
	return head.Activity.State
}

// toolFiles lists the files a tool call reported: the completion carries
// them, a stream that reports them on the start keeps working.
func toolFiles(head ActivityEntry, end *ActivityEntry) []string {
	files := head.Activity.Files
	if end != nil && len(end.Activity.Files) > 0 {
		files = end.Activity.Files
	}
	if len(files) == 0 {
		return nil
	}
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return paths
}

// toolLineText is the one line a tool call renders as: the glyph, the name,
// the argument, the state and the duration. The state comes from the
// completion; without it the call reads as started and keeps a live age.
func toolLineText(head ActivityEntry, end *ActivityEntry, now time.Time) string {
	name := head.Activity.Tool
	if name == "" && end != nil {
		name = end.Activity.Tool
	}
	if name == "" {
		name = "tool unknown"
	}
	state := head.Activity.State
	if end != nil {
		state = end.Activity.State
	}
	parts := []string{firstLine(Sanitize(name))}
	if argument := toolArgument(head, end); argument != "" {
		parts = append(parts, argument)
	}
	parts = append(parts, activityStateLabel(agentio.Activity{Kind: agentio.ActivityKindTool, State: state}))
	parts = append(parts, toolDuration(head, end, now))
	return "⚙ " + strings.Join(parts, " · ")
}

// toolArgument is the argument of the one-line summary: the command of the
// call, or the notable input value a stream reports for other tools.
func toolArgument(head ActivityEntry, end *ActivityEntry) string {
	if head.Activity.Command != "" {
		return firstLine(Sanitize(head.Activity.Command))
	}
	if end != nil && end.Activity.Command != "" {
		return firstLine(Sanitize(end.Activity.Command))
	}
	input := head.Activity.Input
	if len(input) == 0 && end != nil {
		input = end.Activity.Input
	}
	for _, key := range []string{"file_path", "path", "command", "pattern", "query", "url"} {
		if value := strValue(input[key]); value != "" {
			return firstLine(Sanitize(value))
		}
	}
	return ""
}

// toolDuration is the age of a tool call: from its start to its completion,
// or to now while it runs. An unknown time reads as unknown, not as zero.
func toolDuration(head ActivityEntry, end *ActivityEntry, now time.Time) string {
	if head.At.IsZero() || (end == nil && head.Activity.State != agentio.ActivityStateRunning) {
		return "?"
	}
	finish := now
	if end != nil && !end.At.IsZero() {
		finish = end.At
	}
	if finish.After(head.At) {
		return formatUnitDuration(finish.Sub(head.At))
	}
	return "0.0s"
}

// truncatedTailLines names where a message clipped at the in-memory limit
// continues, and shows the open page of its original call log.
func (m Model) truncatedTailLines(unit string, entry ActivityEntry, width int) []string {
	var lines []string
	lines = append(lines, fmt.Sprintf("message text truncated at %d bytes; press ] to read the original call log", activityMessageLimit))
	if page := m.pages[activityPageKey(unit, entry)]; page != nil {
		if page.Err != "" {
			lines = append(lines, "message log: "+page.Err)
		} else if page.Text != "" {
			lines = append(lines, "original call log (page):")
			lines = append(lines, page.Text)
			lines = append(lines, fmt.Sprintf("page %d-%d of %d bytes; [ and ] page; %s", page.Skip, page.End, page.Total, page.Path))
		}
	}
	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, wrapLine(Sanitize(line), width)...)
	}
	return wrapped
}

func entryTimeText(entry ActivityEntry) string {
	if entry.At.IsZero() {
		return "--:--:--"
	}
	return entry.At.Local().Format("15:04:05")
}

func entryHeadline(entry ActivityEntry) string {
	switch entry.Type {
	case "activity":
		label := activityKindLabel(entry.Activity)
		state := activityStateLabel(entry.Activity)
		name := ""
		switch entry.Activity.Kind {
		case agentio.ActivityKindTool:
			name = entry.Activity.Tool
			if name == "" {
				name = "tool unknown"
			}
			if state != "" {
				name += " " + state
			}
		case agentio.ActivityKindUsage:
			name = usageSummary(entry.Activity.Usage)
		}
		executor := routeOfFromActivity(entry.Activity).String()
		if name == "" {
			return label + " · " + executor
		}
		return label + " " + name + " · " + executor
	case "attempt":
		route := routeOf(entry.Fields["route"])
		return fmt.Sprintf("attempt %v · %s", entry.Fields["n"], route.String())
	case "route_switch":
		from, to := routeOf(entry.Fields["from"]), routeOf(entry.Fields["to"])
		kind := strValue(entry.Fields["kind"])
		if kind == "" {
			kind = "unknown reason"
		}
		return fmt.Sprintf("fallback after %s · %s -> %s", kind, from.String(), to.String())
	case "quota":
		name := strValue(entry.Fields["name"])
		if name == "" {
			name = "provider unknown"
		}
		return "quota " + name + " " + quotaSummary(entry.Fields["windows"])
	case "verify":
		stage := strValue(entry.Fields["stage"])
		if stage == "" {
			stage = "verify"
		}
		result := "passed"
		if entryVerifyFailed(entry) {
			result = "failed"
		}
		if truthValue(entry.Fields["reused"]) {
			result += " (reused)"
		}
		code := "unknown"
		if n, ok := intValue(entry.Fields["exit_code"]); ok {
			code = strconv.Itoa(n)
		}
		return fmt.Sprintf("%s %s · exit %s · log %s", stage, result, code, logOrUnknown(entry.Fields["log"]))
	case "commit":
		sha := strValue(entry.Fields["sha"])
		if sha == "" {
			sha = "commit unknown"
		}
		return "commit " + sha
	case "state":
		state := strValue(entry.Fields["state"])
		if state == "" {
			state = "state unknown"
		}
		return fmt.Sprintf("%s · stage %s · attempt %v", state, fieldDefault(entry.Fields["stage"], "unknown"), entry.Fields["attempt"])
	case "unit_start", "unit_end", "run_start", "run_end", "replan", "close", "recovery_conflict", "blocked":
		return entry.Type + " " + entryOutcome(entry)
	}
	return entry.Type
}

func entryOutcome(entry ActivityEntry) string {
	for _, key := range []string{"status", "reason", "result", "state"} {
		if value := strValue(entry.Fields[key]); value != "" {
			return value
		}
	}
	return "recorded"
}

func routeOfFromActivity(activity agentio.Activity) routeField {
	return routeField{Agent: activity.Agent, Model: activity.Model, Effort: activity.Effort}
}

func fieldDefault(value any, fallback string) string {
	if text := strValue(value); text != "" {
		return text
	}
	return fallback
}

func logOrUnknown(value any) string {
	if text := strValue(value); text != "" {
		return text
	}
	return "unknown"
}

func truthValue(value any) bool {
	flag, _ := value.(bool)
	return flag
}

func entryVerifyFailed(entry ActivityEntry) bool {
	if entry.Type != "verify" {
		return false
	}
	code, ok := intValue(entry.Fields["exit_code"])
	return ok && code != 0
}

func quotaSummary(value any) string {
	windows, _ := value.([]any)
	if len(windows) == 0 {
		return "windows unknown"
	}
	var parts []string
	for _, item := range windows {
		window, _ := item.(map[string]any)
		label := strValue(window["label"])
		if label == "" {
			label = "window unknown"
		}
		used := "unknown"
		if n, ok := numberValue(window["used"]); ok {
			used = fmt.Sprintf("%.2f", n)
		}
		reset := "unknown"
		switch value := window["reset"].(type) {
		case nil:
		case string:
			if value != "" {
				reset = value
			}
		default:
			if n, ok := numberValue(value); ok {
				reset = time.Unix(int64(n), 0).UTC().Format(time.RFC3339)
			}
		}
		parts = append(parts, fmt.Sprintf("%s used %s reset %s", label, used, reset))
	}
	return strings.Join(parts, "; ")
}

func usageSummary(usage *agentio.ActivityUsage) string {
	if usage == nil {
		return "usage unknown"
	}
	number := func(value int) string {
		if value == 0 {
			return "unknown"
		}
		return strconv.Itoa(value)
	}
	cost := "unknown"
	if usage.Cost != nil {
		cost = fmt.Sprintf("$%.4f", *usage.Cost)
	}
	return fmt.Sprintf("tokens in %s out %s cache read %s cache write %s reasoning %s total %s cost %s",
		number(usage.Input), number(usage.Output), number(usage.CacheRead), number(usage.CacheWrite),
		number(usage.Reasoning), number(usage.Total), cost)
}

func entryPreview(entry ActivityEntry) string {
	switch entry.Type {
	case "activity":
		if entry.Activity.Command != "" {
			preview := "$ " + firstLine(Sanitize(entry.Activity.Command))
			if result := firstLine(Sanitize(entry.Activity.Result)); result != "" {
				preview += " -> " + result
			}
			return preview
		}
		text := firstLine(Sanitize(entry.Activity.Text))
		if text == "" {
			text = firstLine(Sanitize(entry.Activity.Result))
		}
		if text == "" {
			text = firstLine(Sanitize(entry.Activity.Raw))
		}
		return text
	case "verify":
		return "log " + logOrUnknown(entry.Fields["log"]) + " · press v to open"
	case "quota":
		return "waiting: " + quotaSummary(entry.Fields["windows"])
	}
	return ""
}

func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func (m Model) entryBody(unit string, entry ActivityEntry, width int) []string {
	if entry.Type != "activity" {
		if entry.Type == "quota" {
			return wrapLine(Sanitize(quotaSummary(entry.Fields["windows"])), width)
		}
		if fields := entryFieldsText(entry.Fields); fields != "" {
			return wrapLine(fields, width)
		}
		return nil
	}
	activity := entry.Activity
	var lines []string
	add := func(text string) {
		if text == "" {
			return
		}
		for _, line := range wrapLine(text, width) {
			lines = append(lines, line)
		}
	}
	if activity.Command != "" {
		add("$ " + Sanitize(activity.Command))
	}
	if len(activity.Input) > 0 {
		if raw, err := json.Marshal(activity.Input); err == nil {
			add("input: " + Sanitize(string(raw)))
		} else {
			add("input: unknown (cannot encode)")
		}
	}
	if activity.Kind == agentio.ActivityKindTool {
		key := activityPageKey(unit, entry)
		if page := m.pages[key]; page != nil && page.Err != "" {
			add("result: " + page.Err)
		} else if page := m.pages[key]; page != nil && page.Text != "" {
			if page.Live {
				add("call output so far (not confirmed):")
			} else {
				add("result (page):")
			}
			add(page.Text)
			add(fmt.Sprintf("page %d-%d of %d bytes; [ and ] page, original stays in the call log: %s",
				page.Skip, page.End, page.Total, page.Path))
			if page.Live {
				add(fmt.Sprintf("call still running, %d bytes so far", page.Total))
			}
		} else if activity.State == agentio.ActivityStateRunning {
			add("result: not confirmed yet")
		} else if activity.Result != "" {
			add("result: " + Sanitize(activity.Result))
			if length := activity.Ref.Length; length > len(activity.Result) {
				add(fmt.Sprintf("result clipped at %d bytes; press ] to read the log one page at a time", len(activity.Result)))
			}
		} else if activity.State == agentio.ActivityStateUnknown {
			add("result: unknown (the stream did not confirm an outcome)")
		}
		if len(activity.Files) > 0 {
			add("files reported by the stream (not a git diff):")
			for _, file := range activity.Files {
				kind := file.Kind
				if kind == "" {
					kind = "change kind unknown"
				}
				add("  " + sanitizePath(file.Path) + " (" + kind + ")")
			}
		}
	}
	if activity.Text != "" {
		add(Sanitize(activity.Text))
	}
	if activity.Raw != "" && activity.Kind == agentio.ActivityKindUnknown {
		add("raw: " + Sanitize(activity.Raw))
	}
	if activity.Kind == agentio.ActivityKindStderr && activity.Raw != "" {
		add(Sanitize(activity.Raw))
	}
	if activity.Usage != nil {
		add(usageSummary(activity.Usage))
	}
	if len(lines) == 0 {
		add("(no inline body; original log: " + logOrUnknown(mapValue(entry.Fields, "log")) + ")")
	}
	return lines
}

func mapValue(fields map[string]any, key string) any {
	if fields == nil {
		return nil
	}
	return fields[key]
}

func entryFieldsText(fields map[string]any) string {
	var parts []string
	for _, key := range []string{"reason", "status", "stage", "attempt", "result"} {
		if value, ok := fields[key]; ok {
			parts = append(parts, fmt.Sprintf("%s: %v", key, value))
		}
	}
	return strings.Join(parts, " · ")
}

func sanitizePath(path string) string {
	if path == "" {
		return "path unknown"
	}
	return Sanitize(path)
}

// syncActivityScroll keeps the cursor visible and, while following, keeps the
// newest observation at the bottom of the run stream. The scroll position is
// derived from the rendered section, so plan navigation never changes what a
// live run is doing.
func (m Model) syncActivityScroll() Model {
	m.syncActivityCursor()
	selected, ok := m.selectedRow()
	if !ok || selected.kind != rowUnit {
		return m.clampStreamTop()
	}
	width := m.streamWidth()
	section := m.activitySection(width)
	if len(section.starts) == 0 {
		return m.clampStreamTop()
	}
	cursor := m.activityCursor - section.base
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(section.starts) {
		cursor = len(section.starts) - 1
	}
	offset := len(m.streamHeaderLines(width)) + 1
	start := offset + section.starts[cursor]
	end := offset + section.ends[cursor]
	body := m.streamBodyHeight()
	if m.followActivity {
		m.streamTop = max(0, len(m.streamLines(width))-body)
	} else {
		if start < m.streamTop {
			m.streamTop = start
		}
		if end > m.streamTop+body {
			m.streamTop = min(start, end-body)
		}
	}
	return m.clampStreamTop()
}

// lipglossStyle is the small part of a Lip Gloss style the activity renderer
// uses; it keeps the renderer testable without construction in tests.
type lipglossStyle interface {
	Render(...string) string
}
