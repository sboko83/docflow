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
)

var STYLES = map[string]string{
	"green": "32", "red": "31", "yellow": "33", "dim": "2",
	"bold": "1", "cyan": "36", "low": "90", "medium": "1;34", "high": "1;35",
	"max": "1;38;5;208", "ultra": "1;91", "codex": "1;97;48;2;36;87;166", "claude": "1;97;48;2;193;95;60",
	"white": "97",
	"pi":    "1;37;45", "agent": "1;30;47",
}

var ansiStyleRE = regexp.MustCompile(`\x1b\[[\d;]*m`)

const unverifiedLabel = "Unverified: "

// Progress writes ordinary progress to out and interactive terminal output to
// the same stream. Styles and the spinner are enabled only when out is a TTY.
type Progress struct {
	out      io.Writer
	err      io.Writer
	verbose  bool
	mu       sync.Mutex
	tty      bool
	fancy    bool
	spinner  bool
	colour   bool
	columns  func() int
	label    any
	activity any
	block    bool
	unitID   string
	halt     chan struct{}
	done     chan struct{}
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
	tty := isTerminal(out)
	columns := func() int { return terminalColumns(out) }
	return &Progress{
		out: out, err: err, verbose: verbose, tty: tty,
		fancy: tty && os.Getenv("TERM") != "dumb", spinner: tty && os.Getenv("TERM") != "dumb",
		colour: os.Getenv("NO_COLOR") == "", columns: columns,
	}
}

func (p *Progress) paint(text, style string, stream io.Writer) string {
	if code, ok := STYLES[style]; style != "" && ok && p.colour && p.fancy && isTerminal(stream) {
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
	if p.fancy && p.block && isTerminal(stream) {
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
	} else if p.fancy && isTerminal(stream) {
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
		p.emitLocked(p.paint(end, "dim", p.out), p.out)
	}
	p.block = false
	p.unitID = ""
}

func (p *Progress) openLocked(title string, rounded bool) {
	p.closeLocked()
	p.emitLocked("", p.out)
	prefix := "┌─ "
	if rounded {
		prefix = "╭─ "
	}
	p.wrappedLocked(title, prefix, "bold", p.out, "│  ")
	p.block = true
}

func (p *Progress) start(planID, title string, wall float64, full bool) {
	text := fmt.Sprintf("run: %s %s, start %s", planID, title, clock(wall))
	if full {
		text += ", permissions full"
	}
	if !p.fancy {
		p.line(text, false, "")
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.openLocked("run: "+planID, true)
	p.wrappedLocked(title, "│  ", "", p.out, "")
	ending := "start " + clock(wall)
	if full {
		ending += " · permissions full"
	}
	p.wrappedLocked(ending, "│  ", "dim", p.out, "")
}

func (p *Progress) badge(name string) string {
	style := "agent"
	if name == "codex" || name == "claude" || name == "pi" {
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
	case zaiQuota:
		return "Z.ai"
	default:
		return name
	}
}

func (p *Progress) unit(uid, title string, route agents.Route) {
	if !p.fancy {
		p.line(uid+" "+title+" -> "+routeText(route), false, "")
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.openLocked(uid+": "+title, false)
	p.unitID = uid
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
	parts := []string{p.badge(route.Agent), model, p.paint(effort, level, p.out)}
	row := strings.Join(parts, " │ ")
	if visibleLength(row) <= p.width()-3 {
		p.emitLocked(p.paint("│  ", "dim", p.out)+row, p.out)
	} else {
		p.wrappedLocked(" "+agentName(route.Agent)+" ", "│  ", badgeStyle(route.Agent), p.out, "")
		p.wrappedLocked(model, "│  ", "", p.out, "")
		p.wrappedLocked(effort, "│  ", level, p.out, "")
	}
}

func badgeStyle(name string) string {
	if name == "codex" || name == "claude" || name == "pi" {
		return name
	}
	return "agent"
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

func (p *Progress) elapsed(uid string, seconds float64) {
	if !p.fancy {
		p.line(uid+" time: "+agentio.Duration(seconds), false, "")
		return
	}
	p.line("Time: "+agentio.Duration(seconds), false, "dim")
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

func quotaWindow(window agentio.QuotaWindow, before agentio.Quota, resets bool) (string, string, string) {
	label := window.Label
	if known, ok := quotaLabels[label]; ok {
		label = known
	}
	extra := ""
	for _, prior := range before {
		if prior.Label != window.Label {
			continue
		}
		priorPercent := math.RoundToEven(prior.Used * 100)
		// An idle window at 0% has no running reset yet: the first use opens
		// a window with a new reset, and the delta is still the whole value.
		if priorPercent == 0 || sameReset(prior.Reset, window.Reset) {
			delta := math.RoundToEven(window.Used*100) - priorPercent
			extra += fmt.Sprintf(" (%+.0f)", delta)
			break
		}
	}
	if resets {
		if epoch, ok := numericEpoch(window.Reset); ok {
			layout := "15:04"
			if label != "5h" {
				layout = "Mon 15:04"
			}
			extra += " resets " + time.Unix(int64(epoch), 0).In(time.Local).Format(layout)
		}
	}
	return label, fmt.Sprintf("%.0f%%", window.Used*100), extra
}

func quotaText(quota, before agentio.Quota, resets bool) string {
	parts := make([]string, 0, len(quota))
	for _, window := range quota {
		label, percent, extra := quotaWindow(window, before, resets)
		parts = append(parts, label+" "+percent+extra)
	}
	return strings.Join(parts, " · ")
}

func (p *Progress) quotaRowsLocked(quota, before agentio.Quota, prefix string) {
	width := 10
	if p.width() < 60 {
		width = 6
	}
	rows := make([]string, 0, len(quota))
	resets := make([]string, 0)
	for _, window := range quota {
		label, percent, extra := quotaWindow(window, before, p.verbose)
		colour := "green"
		if window.Used >= .9 {
			colour = "red"
		} else if window.Used >= .75 {
			colour = "yellow"
		}
		if extraBefore, reset, found := strings.Cut(extra, " resets "); found {
			resets = append(resets, label+" resets "+reset)
			extra = extraBefore
		}
		scaled := int(math.RoundToEven(math.Max(0, math.Min(1, window.Used)) * float64(width*8)))
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
		rows = append(rows, p.paint(label, "dim", p.out)+"  "+p.paint(bar, colour, p.out)+p.paint(track, "dim", p.out)+" "+p.paint(fmt.Sprintf("%4s", percent), colour, p.out)+p.paint(extra, "dim", p.out))
	}
	row := ""
	for _, part := range rows {
		if row != "" && visibleLength(row+"    "+part) > p.width()-utf8.RuneCountInString(prefix) {
			p.emitLocked(p.paint(prefix, "dim", p.out)+row, p.out)
			row = ""
		}
		if row != "" {
			row += "    "
		}
		row += part
	}
	if row != "" {
		p.emitLocked(p.paint(prefix, "dim", p.out)+row, p.out)
	}
	for _, reset := range resets {
		p.wrappedLocked(reset, prefix+"  ", "dim", p.out, "")
	}
}

func (p *Progress) quotasLocked(quotas []NamedQuota, before map[string]agentio.Quota, scope string, finish bool) {
	known := make([]NamedQuota, 0, len(quotas))
	for _, item := range quotas {
		if len(item.Quota) > 0 {
			known = append(known, item)
		}
	}
	if !p.fancy {
		shown := make([]string, 0, len(known))
		for _, item := range known {
			shown = append(shown, item.Name+" "+quotaText(item.Quota, before[item.Name], p.verbose))
		}
		if len(shown) > 0 {
			p.lineLocked("quota: "+strings.Join(shown, "; "), p.out, "", false)
		}
		return
	}
	p.clearLocked()
	if finish {
		if len(known) > 0 {
			for i, item := range known {
				prefix := "   "
				if i == 0 {
					prefix = "└─ Quota · used "
				}
				p.emitLocked(p.paint(prefix, "dim", p.out)+p.badge(item.Name), p.out)
				p.quotaRowsLocked(item.Quota, before[item.Name], "   ")
			}
			p.block = false
			p.unitID = ""
		}
		p.closeLocked()
		return
	}
	if len(known) > 0 {
		heading := "Quota · used"
		if scope != "" {
			heading += " · Δ " + scope
		}
		p.wrappedLocked(heading, "│  ", "dim", p.out, "")
		for _, item := range known {
			p.emitLocked(p.paint("│  ", "dim", p.out)+p.badge(item.Name), p.out)
			p.quotaRowsLocked(item.Quota, before[item.Name], "│    ")
		}
	}
}

func (p *Progress) quotas(quotas []NamedQuota, before map[string]agentio.Quota, scope string, finish bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.quotasLocked(quotas, before, scope, finish)
}

func (p *Progress) summary(text string, quotas []NamedQuota, before map[string]agentio.Quota) {
	if !p.fancy {
		shown := make([]string, 0, len(quotas))
		for _, item := range quotas {
			if len(item.Quota) > 0 {
				shown = append(shown, item.Name+" "+quotaText(item.Quota, before[item.Name], false))
			}
		}
		if len(shown) > 0 {
			text += ", " + strings.Join(shown, "; ")
		}
		p.line(text, false, "")
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.openLocked("run: summary", true)
	p.wrappedLocked(strings.TrimPrefix(text, "run: "), "│  ", "", p.out, "")
	p.emitLocked(p.paint("│", "dim", p.out), p.out)
	p.quotasLocked(quotas, before, "run", false)
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
	if p.fancy && p.block && isTerminal(p.err) {
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
