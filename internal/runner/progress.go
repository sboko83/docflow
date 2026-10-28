package runner

import (
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/term"
)

var STYLES = map[string]string{
	"green": "32", "red": "31", "yellow": "33", "dim": "2",
	"bold": "1", "cyan": "36", "low": "90", "medium": "1;34", "high": "1;35",
	"max": "1;38;5;208", "ultra": "1;91", "codex": "1;97;48;2;36;87;166", "claude": "1;97;48;2;193;95;60",
	"white": "97",
	// Pi colours come from the pi.dev logo: coral P and yellow i on its
	// dark background.
	"pi": "1;38;2;234;123;111;48;2;17;21;28", "pi-i": "1;38;2;236;178;71;48;2;17;21;28",
	// Bold 30 renders as grey in many terminals; true-colour black stays black.
	"agent": "1;38;2;0;0;0;47",
}

var ansiStyleRE = regexp.MustCompile(`\x1b\[[\d;]*m`)

const unverifiedLabel = "Unverified: "

// testChangeLabel is the prefix `docflow commit` prints a test change item
// with and the same prefix the run line keeps: one form for the commit
// output, the run output and the parser that reads the items back.
const testChangeLabel = "test change: "

// Progress writes ordinary progress to out and interactive terminal output to
// the same stream. Styles and the spinner are enabled only when out is a TTY.
type Progress struct {
	out       io.Writer
	err       io.Writer
	verbose   bool
	mu        sync.Mutex
	tty       bool
	fancy     bool
	spinner   bool
	colour    bool
	columns   func() int
	label     any
	activity  any
	block     bool
	blockEnd  string
	unitID    string
	unitStart time.Time
	display   agents.Display
	halt      chan struct{}
	done      chan struct{}
}

type NamedQuota struct {
	Name  string
	Quota agentio.Quota
}

func newProgress(out, err io.Writer, verbose bool) *Progress {
	if out == nil {
		out = os.Stdout
	}
	if err == nil {
		err = os.Stderr
	}
	tty := term.IsTerminal(out)
	if file, ok := out.(*os.File); ok && tty {
		// Console hosts may start without ANSI processing enabled.
		term.EnableVTProcessing(file)
	}
	columns := func() int { return terminalColumns(out) }
	return &Progress{
		out: out, err: err, verbose: verbose, tty: tty,
		fancy: tty && os.Getenv("TERM") != "dumb", spinner: tty && os.Getenv("TERM") != "dumb",
		colour: os.Getenv("NO_COLOR") == "", columns: columns,
		display: agents.Display{Quota: agents.QuotaUsed, Resets: true},
	}
}

// setDisplay applies the `[display]` table of the agents config: the quota
// mode and whether the reset time shows.
func (p *Progress) setDisplay(display agents.Display) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.display = display
}

func (p *Progress) paint(text, style string, stream io.Writer) string {
	if code, ok := STYLES[style]; style != "" && ok && p.colour && p.fancy && term.IsTerminal(stream) {
		return "\x1b[" + code + "m" + text + "\x1b[0m"
	}
	return text
}

func (p *Progress) clearLocked() {
	if p.done != nil {
		_, _ = io.WriteString(p.out, "\r\x1b[K")
	}
}

func (p *Progress) line(text string, stderr bool, style string) {
	stream := p.out
	if stderr {
		stream = p.err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lineLocked(text, stream, style, stderr)
}

func (p *Progress) lineLocked(text string, stream io.Writer, style string, stderr bool) {
	p.clearLocked()
	if stderr && style == "" {
		style = "red"
	}
	if p.fancy && p.block && term.IsTerminal(stream) {
		if p.unitID != "" && strings.HasPrefix(text, p.unitID+": ") {
			text = strings.TrimPrefix(text, p.unitID+": ")
		}
		if title, reason, ok := strings.Cut(text, ": "); ok && style == "red" &&
			(strings.HasPrefix(title, "blocked (needs ") || strings.HasPrefix(title, "replan blocked (needs ")) {
			title = strings.Replace(title, "blocked", "Blocked", 1)
			p.wrappedLocked("! "+title+":", "│  ", "red", stream, "")
			p.wrappedLocked(reason, "│    ", "white", stream, "")
			return
		}
		if style == "red" || style == "yellow" {
			text = "! " + text
		}
		p.wrappedLocked(text, "│  ", style, stream, "")
	} else if p.fancy && term.IsTerminal(stream) {
		p.wrappedLocked(text, "", style, stream, "")
	} else {
		p.emitLocked(p.paint(text, style, stream), stream)
	}
}

func (p *Progress) emitLocked(text string, stream io.Writer) {
	if stream == nil {
		stream = p.out
	}
	_, _ = fmt.Fprintln(stream, text)
	if flush, ok := stream.(interface{ Flush() error }); ok {
		_ = flush.Flush()
	}
}

func (p *Progress) width() int {
	width := 80
	if p.columns != nil {
		width = p.columns()
	}
	width--
	if width < 20 {
		return 20
	}
	return width
}

func visibleLength(text string) int {
	return utf8.RuneCountInString(ansiStyleRE.ReplaceAllString(text, ""))
}

// wrapParagraph ports Python textwrap.wrap(text, width, break_on_hyphens=False)
// used by the Python runner: tabs expand to 8 columns, only ASCII whitespace
// separates words, the paragraph's leading whitespace is kept, and a long word
// fills the rest of the current row before it breaks.
func wrapParagraph(text string, width int) []string {
	chunks := wrapChunks(expandTabs(text))
	lines := make([]string, 0)
	for len(chunks) > 0 {
		line := make([]string, 0)
		lineLen := 0
		if len(lines) > 0 && strings.TrimSpace(chunks[0]) == "" {
			chunks = chunks[1:]
		}
		for len(chunks) > 0 {
			size := utf8.RuneCountInString(chunks[0])
			if lineLen+size > width {
				break
			}
			line = append(line, chunks[0])
			lineLen += size
			chunks = chunks[1:]
		}
		if len(chunks) > 0 && utf8.RuneCountInString(chunks[0]) > width {
			room := width - lineLen
			if width < 1 {
				room = 1
			}
			head := []rune(chunks[0])
			line = append(line, string(head[:room]))
			chunks[0] = string(head[room:])
		}
		if len(line) > 0 && strings.TrimSpace(line[len(line)-1]) == "" {
			line = line[:len(line)-1]
		}
		if len(line) > 0 {
			lines = append(lines, strings.Join(line, ""))
		}
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// expandTabs mirrors Python str.expandtabs(8).
func expandTabs(text string) string {
	var out strings.Builder
	column := 0
	for _, char := range text {
		switch char {
		case '\t':
			spaces := 8 - column%8
			out.WriteString(strings.Repeat(" ", spaces))
			column += spaces
		case '\n', '\r':
			out.WriteRune(char)
			column = 0
		default:
			out.WriteRune(char)
			column++
		}
	}
	return out.String()
}

// wrapChunks splits text into alternating runs of words and spaces; every
// ASCII whitespace character becomes one space, as textwrap does.
func wrapChunks(text string) []string {
	chunks := make([]string, 0)
	var current strings.Builder
	inSpace := false
	for _, char := range text {
		space := strings.ContainsRune(" \t\n\v\f\r", char)
		if current.Len() > 0 && space != inSpace {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		inSpace = space
		if space {
			current.WriteByte(' ')
		} else {
			current.WriteRune(char)
		}
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

func (p *Progress) wrappedLocked(text, prefix, style string, stream io.Writer, continuation string) {
	if stream == nil {
		stream = p.out
	}
	for _, paragraph := range strings.Split(text, "\n") {
		rows := wrapParagraph(paragraph, p.width()-utf8.RuneCountInString(prefix))
		for _, row := range rows {
			p.emitLocked(p.paint(prefix, "dim", stream)+p.paint(row, style, stream), stream)
			if continuation != "" {
				prefix = continuation
			}
		}
	}
}

func (p *Progress) closeLocked() {
	p.clearLocked()
	if p.block {
		end := "╰─"
		if p.unitID != "" {
			end = "└─"
		}
		if p.blockEnd != "" {
			end += " " + p.blockEnd
		}
		p.emitLocked(p.paint(end, "dim", p.out), p.out)
	}
	p.block = false
	p.blockEnd = ""
	p.unitID = ""
}

// openLocked starts a frame. A stamp (the block's start time) sits dim in
// front of the title and wraps under the frame; rounded frames (the run start
// and the summary) carry no stamp.
func (p *Progress) openLocked(title string, rounded bool, stamp string) {
	p.closeLocked()
	p.emitLocked("", p.out)
	prefix := "┌─ "
	if rounded {
		prefix = "╭─ "
	}
	if stamp == "" {
		p.wrappedLocked(title, prefix, "bold", p.out, "│  ")
		p.block = true
		return
	}
	head := prefix + stamp + " · "
	for index, row := range wrapParagraph(title, p.width()-utf8.RuneCountInString(head)) {
		left := head
		if index > 0 {
			left = "│  "
		}
		p.emitLocked(p.paint(left, "dim", p.out)+p.paint(row, "bold", p.out), p.out)
	}
	p.block = true
}

func (p *Progress) start(planID, title string, wall float64, full bool) {
	text := fmt.Sprintf("run: %s %s, Start at %s", planID, title, clock(wall))
	if full {
		text += ", permissions full"
	}
	if !p.fancy {
		p.line(text, false, "")
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.openLocked("run: "+planID+" "+title, true, "")
	p.blockEnd = "Go! Go! Go!"
	ending := "Start at " + clock(wall)
	if full {
		ending += " · permissions full"
	}
	p.wrappedLocked(ending, "│  ", "dim", p.out, "")
}

func (p *Progress) badge(name string) string {
	if name == "pi" {
		return p.paint(" P", "pi", p.out) + p.paint("i ", "pi-i", p.out)
	}
	style := "agent"
	if name == "codex" || name == "claude" {
		style = name
	}
	return p.paint(" "+agentName(name)+" ", style, p.out)
}

func agentName(name string) string {
	switch name {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude"
	case "pi":
		return "Pi"
	case zaiQuota:
		return "Z.ai"
	case localQuota:
		return "Local"
	default:
		return name
	}
}

func (p *Progress) unit(uid, title string, route agents.Route, start time.Time) {
	if !p.fancy {
		routeLabel := routeText(route)
		if isLocalRoute(route) {
			routeLabel = "Local: " + routeLabel
		}
		p.line(uid+" "+title+" -> "+routeLabel, false, "")
		p.mu.Lock()
		p.unitStart = start
		p.mu.Unlock()
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.openLocked(uid+": "+title, false, hour(epoch(start)))
	p.unitID = uid
	p.unitStart = start
	p.executorLocked(route)
}

func (p *Progress) executor(route agents.Route) {
	if !p.fancy {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.executorLocked(route)
}

func (p *Progress) executorLocked(route agents.Route) {
	level := "dim"
	switch route.Effort {
	case "low":
		level = "low"
	case "medium":
		level = "medium"
	case "high":
		level = "high"
	case "xhigh", "max":
		level = "max"
	case "ultra":
		level = "ultra"
	}
	model, effort := route.Model, route.Effort
	if model == "" {
		model = "-"
	}
	if effort == "" {
		effort = "-"
	}
	badge := route.Agent
	if isLocalRoute(route) {
		badge = localQuota
	}
	parts := []string{p.badge(badge), model, p.paint(effort, level, p.out)}
	row := strings.Join(parts, " │ ")
	if visibleLength(row) <= p.width()-3 {
		p.emitLocked(p.paint("│  ", "dim", p.out)+row, p.out)
	} else {
		p.emitLocked(p.paint("│  ", "dim", p.out)+p.badge(badge), p.out)
		p.wrappedLocked(model, "│  ", "", p.out, "")
		p.wrappedLocked(effort, "│  ", level, p.out, "")
	}
}

func routeText(route agents.Route) string {
	return agents.RouteText(route)
}

func (p *Progress) result(uid, status, text string) {
	if !p.fancy {
		p.line(uid+" "+status+": "+text, false, "green")
		return
	}
	symbol, style := "✓", "green"
	if status == "replanned" {
		symbol, style = "↻", "cyan"
	}
	label := status
	if label != "" {
		label = strings.ToUpper(label[:1]) + label[1:]
	}
	p.line(symbol+" "+label+": "+text, false, style)
}

func (p *Progress) unverified(text string) {
	if !p.fancy {
		p.line("  "+unverifiedLabel+text, false, "yellow")
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearLocked()
	p.wrappedLocked("! Unverified:", "│  ", "yellow", p.out, "")
	p.wrappedLocked(text, "│    ", "", p.out, "")
}

// testChanges prints the test change items of the unit's commit — the tests
// its commit marks as weakened — under the unit in the style of Unverified.
// A unit without such items prints nothing.
func (p *Progress) testChanges(items []string) {
	if len(items) == 0 {
		return
	}
	if !p.fancy {
		for _, item := range items {
			p.line("  "+testChangeLabel+item, false, "yellow")
		}
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearLocked()
	p.wrappedLocked("! Test changes:", "│  ", "yellow", p.out, "")
	for _, item := range items {
		p.wrappedLocked(item, "│    ", "", p.out, "")
	}
}

// resume reports an attempt the time cap ended while the agent was still
// working: the run keeps the session and tries again, so the line is yellow
// without the "!" mark that names a problem.
func (p *Progress) resume(text string) {
	p.line(p.paint(text, "yellow", p.out), false, "")
}

// unitEnd closes the block a unit or replan opened: in a terminal the frame's
// last line carries the end time and the block's duration, in text output the
// same fact is one line naming the span. A block that never opened prints
// nothing.
func (p *Progress) unitEnd(uid string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	start := p.unitStart
	if start.IsZero() {
		return
	}
	p.unitStart = time.Time{}
	seconds := time.Since(start).Seconds()
	if !p.fancy {
		p.lineLocked(fmt.Sprintf("%s time: %s (%s–%s)", uid, agentio.Duration(seconds),
			clock(epoch(start)), clock(epoch(start)+seconds)), p.out, "", false)
		return
	}
	if !p.block {
		return
	}
	p.clearLocked()
	// The end time is the header's start plus the duration: one span, read off
	// the frame's first and last row.
	p.emitLocked(p.paint("└─ "+hour(epoch(start)+seconds)+" · ", "dim", p.out)+
		p.paint(agentio.Duration(seconds), "dim", p.out), p.out)
	p.block = false
	p.unitID = ""
}

func count(n int) string {
	if n >= 100_000 {
		return fmt.Sprintf("%.0fk", float64(n)/1000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

func clock(epoch float64) string {
	return time.Unix(int64(epoch), 0).In(time.Local).Format("15:04:05")
}

// hour is the resolution a block's span reads at: the frame's first row names
// the start, its last row the end.
func hour(epoch float64) string {
	return time.Unix(int64(epoch), 0).In(time.Local).Format("15:04")
}

var quotaLabels = map[string]string{"five_hour": "5h", "seven_day": "7d"}

const resetSlack = 600.0

func sameReset(a, b any) bool {
	left, leftOK := numericEpoch(a)
	right, rightOK := numericEpoch(b)
	if leftOK && rightOK {
		return math.Abs(left-right) <= resetSlack
	}
	return equalValues(a, b)
}

func numericEpoch(value any) (float64, bool) {
	var number float64
	switch v := value.(type) {
	case int:
		number = float64(v)
	case int8:
		number = float64(v)
	case int16:
		number = float64(v)
	case int32:
		number = float64(v)
	case int64:
		number = float64(v)
	case uint:
		number = float64(v)
	case uint8:
		number = float64(v)
	case uint16:
		number = float64(v)
	case uint32:
		number = float64(v)
	case uint64:
		number = float64(v)
	case float32:
		number = float64(v)
	case float64:
		number = v
	default:
		return 0, false
	}
	if math.Abs(number) >= 1e12 {
		number /= 1000
	}
	return number, true
}

func equalValues(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	left, leftOK := numericEpoch(a)
	right, rightOK := numericEpoch(b)
	if leftOK && rightOK {
		return left == right
	}
	return fmt.Sprintf("%T:%v", a, a) == fmt.Sprintf("%T:%v", b, b)
}

// quotaView is one quota window as the display shows it. `used` shows the
// spent share, `left` shows 100 − used; the bar, the percent and the delta all
// carry the mode, so a `left` delta is the mirror of the `used` one.
type quotaView struct {
	label   string
	percent string
	shown   int
	delta   string
	reset   string
}

func quotaViewOf(window agentio.QuotaWindow, before agentio.Quota, left bool) quotaView {
	label := window.Label
	if known, ok := quotaLabels[label]; ok {
		label = known
	}
	raw := int(math.RoundToEven(window.Used * 100))
	view := quotaView{label: label, shown: raw}
	for _, prior := range before {
		if prior.Label != window.Label {
			continue
		}
		priorPercent := int(math.RoundToEven(prior.Used * 100))
		// An idle window at 0% has no running reset yet: the first use opens
		// a window with a new reset, and the delta is still the whole value.
		if priorPercent == 0 || sameReset(prior.Reset, window.Reset) {
			delta := raw - priorPercent
			if left {
				delta = -delta
			}
			view.delta = fmt.Sprintf(" (%+d)", delta)
			break
		}
	}
	if epoch, ok := numericEpoch(window.Reset); ok {
		layout := "15:04"
		if label != "5h" {
			layout = "Mon 15:04"
		}
		view.reset = time.Unix(int64(epoch), 0).In(time.Local).Format(layout)
	}
	if left {
		view.shown = 100 - raw
	}
	view.percent = fmt.Sprintf("%d%%", view.shown)
	return view
}

// quotaColour paints by the shown percent: in `used` a rising number is the
// problem, in `left` a falling one is.
func quotaColour(shown int, left bool) string {
	if left {
		switch {
		case shown > 50:
			return "green"
		case shown > 20:
			return "yellow"
		default:
			return "red"
		}
	}
	switch {
	case shown >= 80:
		return "red"
	case shown >= 50:
		return "yellow"
	default:
		return "green"
	}
}

func quotaText(quota, before agentio.Quota, display agents.Display, resets bool) string {
	parts := make([]string, 0, len(quota))
	for _, window := range quota {
		view := quotaViewOf(window, before, display.Quota == agents.QuotaLeft)
		text := view.label + " " + view.percent + view.delta
		if resets && view.reset != "" {
			text += " resets " + view.reset
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, " · ")
}

// quotaRow is one quota window's TTY row. `reset` is the inline reset moved to
// its own line: a window wider than the frame would otherwise push `↻…` onto a
// next line with the same prefix, breaking the frame's right edge.
type quotaRow struct {
	text  string
	reset string
	gap   int
}

// quotaRowsOf builds one agent's quota cells, one per window.
func (p *Progress) quotaRowsOf(quota, before agentio.Quota, prefix string) []quotaRow {
	width := 10
	if p.width() < 60 {
		width = 6
	}
	left := p.display.Quota == agents.QuotaLeft
	room := p.width() - utf8.RuneCountInString(prefix)
	rows := make([]quotaRow, 0, len(quota))
	for _, window := range quota {
		view := quotaViewOf(window, before, left)
		colour := quotaColour(view.shown, left)
		scaled := int(math.RoundToEven(math.Max(0, math.Min(1, float64(view.shown)/100)) * float64(width*8)))
		full, fraction := scaled/8, scaled%8
		bar := strings.Repeat("█", full)
		fractions := []rune("▏▎▍▌▋▊▉")
		if fraction > 0 {
			bar += string(fractions[fraction-1])
		}
		trackCount := width - full
		if fraction > 0 {
			trackCount--
		}
		if trackCount < 0 {
			trackCount = 0
		}
		track := strings.Repeat("░", trackCount)
		head := p.paint(view.label, "dim", p.out) + "  "
		row := quotaRow{
			text: head + p.paint(bar, colour, p.out) + p.paint(track, "dim", p.out) + " " +
				p.paint(fmt.Sprintf("%4s", view.percent), colour, p.out) + p.paint(view.delta, "dim", p.out),
		}
		if p.display.Resets && view.reset != "" {
			reset := p.paint("↻ "+view.reset, "dim", p.out)
			if visibleLength(row.text+" "+reset) <= room {
				row.text += " " + reset
			} else {
				// The row without the reset stays on its line, the reset follows
				// under the bar. A row too wide even without the reset is emitted
				// as is: no wrap loop, no lost text.
				row.reset = reset
				row.gap = visibleLength(head)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// quotaColumnWidths is the widest cell of each window column across agents,
// so a window without a reset time does not shift the next column left.
func quotaColumnWidths(groups [][]quotaRow) []int {
	widths := []int{}
	for _, rows := range groups {
		for i, row := range rows {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			if row.reset == "" && visibleLength(row.text) > widths[i] {
				widths[i] = visibleLength(row.text)
			}
		}
	}
	return widths
}

// quotaRowsLocked emits one agent's cells, padding each joined cell to its
// column width.
func (p *Progress) quotaRowsLocked(rows []quotaRow, widths []int, prefix string) {
	room := p.width() - utf8.RuneCountInString(prefix)
	row := ""
	pad := ""
	for i, part := range rows {
		if row != "" && visibleLength(row+pad+"    "+part.text) > room {
			p.emitLocked(p.paint(prefix, "dim", p.out)+row, p.out)
			row = ""
		}
		if row != "" {
			row += pad + "    "
		}
		row += part.text
		pad = ""
		if i < len(widths) && widths[i] > visibleLength(part.text) {
			pad = strings.Repeat(" ", widths[i]-visibleLength(part.text))
		}
		if part.reset == "" {
			continue
		}
		emit := func(text string) { p.emitLocked(p.paint(prefix, "dim", p.out)+text, p.out) }
		emit(row)
		emit(strings.Repeat(" ", part.gap) + part.reset)
		row = ""
	}
	if row != "" {
		p.emitLocked(p.paint(prefix, "dim", p.out)+row, p.out)
	}
}

// quotasLocked writes the quota rows inside the open frame. It never closes
// the frame: the block's owner ends it, with a duration line for a unit and
// replan block and the bottom border for the start and the summary frame.
func (p *Progress) quotasLocked(quotas []NamedQuota, before map[string]agentio.Quota, scope string) {
	known := make([]NamedQuota, 0, len(quotas))
	for _, item := range quotas {
		if len(item.Quota) > 0 {
			known = append(known, item)
		}
	}
	if !p.fancy {
		shown := make([]string, 0, len(known))
		for _, item := range known {
			shown = append(shown, item.Name+" "+quotaText(item.Quota, before[item.Name], p.display, p.verbose && p.display.Resets))
		}
		if len(shown) > 0 {
			p.lineLocked("quota: "+strings.Join(shown, "; "), p.out, "", false)
		}
		return
	}
	p.clearLocked()
	if len(known) == 0 {
		return
	}
	heading := "Quota · " + p.display.Quota
	if scope != "" {
		heading += " · Δ " + scope
	}
	groups := make([][]quotaRow, len(known))
	for i, item := range known {
		groups[i] = p.quotaRowsOf(item.Quota, before[item.Name], "│  ")
	}
	widths := quotaColumnWidths(groups)
	first := 0
	if scope == "" && len(known) == 1 {
		// One agent's badge fits the heading row.
		p.emitLocked(p.paint("│  "+heading+" ", "dim", p.out)+p.badge(known[0].Name), p.out)
		p.quotaRowsLocked(groups[0], widths, "│  ")
		first = 1
	} else {
		p.wrappedLocked(heading, "│  ", "dim", p.out, "")
	}
	for i := first; i < len(known); i++ {
		p.emitLocked(p.paint("│  ", "dim", p.out)+p.badge(known[i].Name), p.out)
		p.quotaRowsLocked(groups[i], widths, "│  ")
	}
}

func (p *Progress) quotas(quotas []NamedQuota, before map[string]agentio.Quota, scope string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.quotasLocked(quotas, before, scope)
}

func (p *Progress) summary(text string, quotas []NamedQuota, before map[string]agentio.Quota, hints []string) {
	if !p.fancy {
		shown := make([]string, 0, len(quotas))
		for _, item := range quotas {
			if len(item.Quota) > 0 {
				shown = append(shown, item.Name+" "+quotaText(item.Quota, before[item.Name], p.display, false))
			}
		}
		if len(shown) > 0 {
			text += ", " + strings.Join(shown, "; ")
		}
		p.line(text, false, "")
		for _, hint := range hints {
			p.line("hint: "+hint, false, "yellow")
		}
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.openLocked("run: summary", true, "")
	p.wrappedLocked(strings.TrimPrefix(text, "run: "), "│  ", "", p.out, "")
	p.emitLocked(p.paint("│", "dim", p.out), p.out)
	p.quotasLocked(quotas, before, "run")
	for _, hint := range hints {
		p.wrappedLocked("hint: "+hint, "│  ", "yellow", p.out, "")
	}
	p.closeLocked()
}

// output prints the captured output of a docflow subcommand to stderr. Inside a
// TTY unit block it keeps the frame and wraps to the terminal width; elsewhere
// it is written verbatim.
func (p *Progress) output(text string) {
	if text == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearLocked()
	if p.fancy && p.block && term.IsTerminal(p.err) {
		p.wrappedLocked(strings.TrimRight(text, "\n"), "│  ", "", p.err, "")
		return
	}
	_, _ = io.WriteString(p.err, text)
}

func (p *Progress) detail(text string) {
	if p.verbose {
		p.line("  "+text, false, "dim")
	}
}

func (p *Progress) drawLocked(frame string) {
	label := displayValue(p.label)
	text := frame + " " + label
	if p.verbose && p.activity != nil {
		if extra := displayValue(p.activity); extra != "" {
			text += " · " + extra
		}
	}
	width := 80
	if p.columns != nil {
		width = p.columns()
	}
	if p.block {
		text = "│  " + text
	}
	text = truncateRunes(text, maxInt(width-1, 1))
	_, _ = io.WriteString(p.out, "\r\x1b[K"+text)
	flushWriter(p.out)
}

func displayValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case func() string:
		return v()
	default:
		return fmt.Sprint(v)
	}
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return text
}

func flushWriter(writer io.Writer) {
	if flush, ok := writer.(interface{ Flush() error }); ok {
		_ = flush.Flush()
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (p *Progress) spin(label any, activity any) {
	p.stop()
	p.mu.Lock()
	if !p.spinner {
		p.mu.Unlock()
		return
	}
	p.label, p.activity = label, activity
	halt, done := make(chan struct{}), make(chan struct{})
	p.halt, p.done = halt, done
	p.mu.Unlock()
	go func() {
		defer close(done)
		frames := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		index := 0
		for {
			p.mu.Lock()
			if p.halt == halt {
				select {
				case <-halt:
					p.mu.Unlock()
					return
				default:
					p.drawLocked(string(frames[index%len(frames)]))
					index++
				}
			}
			p.mu.Unlock()
			select {
			case <-halt:
				return
			case <-ticker.C:
			}
		}
	}()
}

func (p *Progress) stop() {
	p.mu.Lock()
	done, halt := p.done, p.halt
	if done == nil {
		p.mu.Unlock()
		return
	}
	if halt != nil {
		close(halt)
		p.halt = nil
	}
	p.mu.Unlock()
	<-done
	p.mu.Lock()
	if p.done == done {
		p.clearLocked()
		flushWriter(p.out)
		p.done = nil
		p.halt = nil
	}
	p.mu.Unlock()
}

func (p *Progress) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeLocked()
}
