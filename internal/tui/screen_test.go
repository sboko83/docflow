package tui

import (
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"bsv-docflow/internal/runner"
)

// acceptTerminal reconstructs the visible screen from the PTY byte stream.
// Bubble Tea renders a full-screen diff: a raw capture contains only changed
// suffixes and cursor moves, so text assertions must read the screen, not the
// byte sequence. The emulator covers the CSI subset the renderer and the
// workspace use; unknown private modes are ignored.
type acceptTerminal struct {
	width, height  int
	grid           [][]rune
	x, y           int
	top, bottom    int
	wrap           bool
	last           rune
	pending        []byte
	savedX, savedY int
}

func newAcceptTerminal(width, height int) *acceptTerminal {
	t := &acceptTerminal{wrap: true}
	t.resize(width, height)
	return t
}

func (t *acceptTerminal) resize(width, height int) {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	t.width, t.height = width, height
	t.grid = make([][]rune, height)
	for i := range t.grid {
		t.grid[i] = acceptBlankLine(width)
	}
	t.x, t.y = 0, 0
	t.top, t.bottom = 0, height-1
}

func acceptBlankLine(width int) []rune {
	line := make([]rune, width)
	for i := range line {
		line[i] = ' '
	}
	return line
}

func (t *acceptTerminal) String() string {
	lines := make([]string, 0, len(t.grid))
	for _, line := range t.grid {
		var b strings.Builder
		for _, r := range line {
			if r == 0 {
				continue
			}
			b.WriteRune(r)
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	return strings.Join(lines, "\n")
}

func acceptRuneWidth(r rune) int {
	if r == 0 || r < 32 || (r >= 0x7f && r < 0xa0) {
		return 0
	}
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) {
		return 0
	}
	switch {
	case r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) ||
		(r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe10 && r <= 0xfe19) ||
		(r >= 0xfe30 && r <= 0xfe6f) ||
		(r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) ||
		(r >= 0x1f300 && r <= 0x1f64f) ||
		(r >= 0x1f900 && r <= 0x1f9ff) ||
		(r >= 0x20000 && r <= 0x3fffd)):
		return 2
	}
	return 1
}

func (t *acceptTerminal) feed(data []byte) {
	t.pending = append(t.pending, data...)
	for len(t.pending) > 0 {
		if t.pending[0] == 0x1b {
			consumed, ok := t.escape(t.pending)
			if !ok {
				return
			}
			t.pending = t.pending[consumed:]
			continue
		}
		r, size := utf8.DecodeRune(t.pending)
		if r == utf8.RuneError && size == 1 {
			if !utf8.FullRune(t.pending) {
				return
			}
			// An invalid byte is not printable output; drop it.
			t.pending = t.pending[1:]
			continue
		}
		t.pending = t.pending[size:]
		t.control(r)
	}
}

func (t *acceptTerminal) control(r rune) {
	switch r {
	case '\x00', '\x07':
		return
	case '\r':
		t.x = 0
	case '\n':
		t.lineFeed()
	case '\b':
		if t.x > 0 {
			t.x--
		}
	case '\t':
		t.x = (t.x/8 + 1) * 8
		if t.x >= t.width {
			t.x = t.width - 1
		}
	default:
		t.put(r)
	}
}

func (t *acceptTerminal) put(r rune) {
	width := acceptRuneWidth(r)
	if width == 0 {
		return
	}
	if t.x >= t.width {
		if t.wrap {
			t.lineFeed()
			t.x = 0
		} else {
			t.x = t.width - 1
		}
	}
	if t.y < 0 || t.y >= t.height {
		return
	}
	t.grid[t.y][t.x] = r
	if width == 2 && t.x+1 < t.width {
		t.grid[t.y][t.x+1] = 0
	}
	t.last = r
	t.x += width
}

func (t *acceptTerminal) lineFeed() {
	if t.y == t.bottom {
		t.scrollUp(1)
		return
	}
	if t.y < t.height-1 {
		t.y++
	}
}

func (t *acceptTerminal) line(index int) []rune {
	if index < 0 || index >= t.height {
		return acceptBlankLine(t.width)
	}
	return t.grid[index]
}

func (t *acceptTerminal) scrollUp(n int) {
	if n < 1 {
		n = 1
	}
	if n > t.bottom-t.top+1 {
		n = t.bottom - t.top + 1
	}
	for i := 0; i < n; i++ {
		copy(t.grid[t.top:t.bottom], t.grid[t.top+1:t.bottom+1])
		t.grid[t.bottom] = acceptBlankLine(t.width)
	}
}

func (t *acceptTerminal) scrollDown(n int) {
	if n < 1 {
		n = 1
	}
	if n > t.bottom-t.top+1 {
		n = t.bottom - t.top + 1
	}
	for i := 0; i < n; i++ {
		copy(t.grid[t.top+1:t.bottom+1], t.grid[t.top:t.bottom])
		t.grid[t.top] = acceptBlankLine(t.width)
	}
}

func (t *acceptTerminal) eraseLine(mode int) {
	if t.y < 0 || t.y >= t.height {
		return
	}
	line := t.grid[t.y]
	switch mode {
	case 1:
		for i := 0; i <= t.x && i < len(line); i++ {
			line[i] = ' '
		}
	case 2:
		for i := range line {
			line[i] = ' '
		}
	default:
		for i := t.x; i < len(line); i++ {
			line[i] = ' '
		}
	}
}

func (t *acceptTerminal) eraseDisplay(mode int) {
	switch mode {
	case 1:
		for i := 0; i < t.y; i++ {
			t.grid[i] = acceptBlankLine(t.width)
		}
		t.eraseLine(1)
	case 2, 3:
		for i := range t.grid {
			t.grid[i] = acceptBlankLine(t.width)
		}
	default:
		t.eraseLine(0)
		for i := t.y + 1; i < t.height; i++ {
			t.grid[i] = acceptBlankLine(t.width)
		}
	}
}

func (t *acceptTerminal) deleteChars(n int) {
	if n < 1 {
		n = 1
	}
	if t.y < 0 || t.y >= t.height {
		return
	}
	line := t.grid[t.y]
	for i := t.x; i < len(line); i++ {
		if i+n < len(line) {
			line[i] = line[i+n]
		} else {
			line[i] = ' '
		}
	}
}

func (t *acceptTerminal) insertChars(n int) {
	if n < 1 {
		n = 1
	}
	if t.y < 0 || t.y >= t.height {
		return
	}
	line := t.grid[t.y]
	for i := len(line) - 1; i >= t.x; i-- {
		if i-n >= t.x {
			line[i] = line[i-n]
		} else {
			line[i] = ' '
		}
	}
}

func (t *acceptTerminal) eraseChars(n int) {
	if n < 1 {
		n = 1
	}
	if t.y < 0 || t.y >= t.height {
		return
	}
	line := t.grid[t.y]
	for i := t.x; i < t.x+n && i < len(line); i++ {
		line[i] = ' '
	}
}

func (t *acceptTerminal) insertLines(n int) {
	if n < 1 {
		n = 1
	}
	if t.y < t.top || t.y > t.bottom {
		return
	}
	for i := t.bottom; i >= t.y; i-- {
		if i-n >= t.y {
			t.grid[i] = t.grid[i-n]
		} else {
			t.grid[i] = acceptBlankLine(t.width)
		}
	}
}

func (t *acceptTerminal) deleteLines(n int) {
	if n < 1 {
		n = 1
	}
	if t.y < t.top || t.y > t.bottom {
		return
	}
	for i := t.y; i <= t.bottom; i++ {
		if i+n <= t.bottom {
			t.grid[i] = t.grid[i+n]
		} else {
			t.grid[i] = acceptBlankLine(t.width)
		}
	}
}

func (t *acceptTerminal) escape(data []byte) (int, bool) {
	if len(data) < 2 {
		return 0, false
	}
	switch data[1] {
	case '[':
		for i := 2; i < len(data); i++ {
			if data[i] >= 0x40 && data[i] <= 0x7e {
				t.csi(string(data[2:i]), data[i])
				return i + 1, true
			}
		}
		return 0, false
	case ']':
		for i := 2; i < len(data); i++ {
			if data[i] == 0x07 {
				return i + 1, true
			}
			if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '\\' {
				return i + 2, true
			}
		}
		return 0, false
	case '7':
		t.savedX, t.savedY = t.x, t.y
		return 2, true
	case '8':
		t.x, t.y = t.savedX, t.savedY
		return 2, true
	case 'M':
		if t.y == t.top {
			t.scrollDown(1)
		} else if t.y > 0 {
			t.y--
		}
		return 2, true
	case 'D', 'E':
		t.lineFeed()
		if data[1] == 'E' {
			t.x = 0
		}
		return 2, true
	case '(', ')', '*', '+':
		if len(data) < 3 {
			return 0, false
		}
		return 3, true
	}
	return 2, true
}

func acceptCSIparam(params []int, index, fallback int) int {
	if index >= len(params) || params[index] == 0 {
		return fallback
	}
	return params[index]
}

func (t *acceptTerminal) csi(text string, final byte) {
	private := byte(0)
	if len(text) > 0 && strings.ContainsRune("?><=!", rune(text[0])) {
		private = text[0]
		text = text[1:]
	}
	var params []int
	for _, part := range strings.Split(text, ";") {
		part = strings.TrimSpace(part)
		value := 0
		if part != "" {
			if n, err := strconv.Atoi(part); err == nil {
				value = n
			}
		}
		params = append(params, value)
	}
	if private == '?' {
		switch final {
		case 'h', 'l':
			for _, p := range params {
				switch p {
				case 7:
					t.wrap = final == 'h'
				case 1049:
					if final == 'h' {
						t.resize(t.width, t.height)
					}
				}
			}
		}
		return
	}
	if private != 0 {
		return
	}
	switch final {
	case 'H', 'f':
		t.y = acceptCSIparam(params, 0, 1) - 1
		t.x = acceptCSIparam(params, 1, 1) - 1
	case 'd':
		t.y = acceptCSIparam(params, 0, 1) - 1
	case 'G', '`':
		t.x = acceptCSIparam(params, 0, 1) - 1
	case 'A':
		t.y -= acceptCSIparam(params, 0, 1)
	case 'B':
		t.y += acceptCSIparam(params, 0, 1)
	case 'C':
		t.x += acceptCSIparam(params, 0, 1)
	case 'D':
		t.x -= acceptCSIparam(params, 0, 1)
	case 'E':
		t.y += acceptCSIparam(params, 0, 1)
		t.x = 0
	case 'F':
		t.y -= acceptCSIparam(params, 0, 1)
		t.x = 0
	case 'e':
		t.y += acceptCSIparam(params, 0, 1)
	case 'J':
		t.eraseDisplay(paramsValue(params))
	case 'K':
		t.eraseLine(paramsValue(params))
	case 'P':
		t.deleteChars(acceptCSIparam(params, 0, 1))
	case '@':
		t.insertChars(acceptCSIparam(params, 0, 1))
	case 'X':
		t.eraseChars(acceptCSIparam(params, 0, 1))
	case 'S':
		t.scrollUp(acceptCSIparam(params, 0, 1))
	case 'T':
		t.scrollDown(acceptCSIparam(params, 0, 1))
	case 'L':
		t.insertLines(acceptCSIparam(params, 0, 1))
	case 'M':
		t.deleteLines(acceptCSIparam(params, 0, 1))
	case 'r':
		t.top = acceptCSIparam(params, 0, 1) - 1
		t.bottom = acceptCSIparam(params, 1, t.height) - 1
		if t.top < 0 {
			t.top = 0
		}
		if t.bottom >= t.height {
			t.bottom = t.height - 1
		}
		if t.top > t.bottom {
			t.top, t.bottom = 0, t.height-1
		}
		t.x, t.y = 0, 0
	case 'b':
		if t.last != 0 {
			n := acceptCSIparam(params, 0, 1)
			for i := 0; i < n; i++ {
				t.put(t.last)
			}
		}
	case 's':
		t.savedX, t.savedY = t.x, t.y
	case 'u':
		t.x, t.y = t.savedX, t.savedY
	}
	if t.x < 0 {
		t.x = 0
	}
	if t.y < 0 {
		t.y = 0
	}
	if t.y >= t.height {
		t.y = t.height - 1
	}
}

func paramsValue(params []int) int {
	if len(params) == 0 {
		return 0
	}
	return params[0]
}

// TestScreenKeysSwitchScreens covers the four tabs: 1-4 move between the
// screens from the tree, from the unit card and from an open help, and a
// switch back to the project lands on the tree.
func TestScreenKeysSwitchScreens(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	if model.screen != screenProject {
		t.Fatalf("initial screen = %v, want the project", model.screen)
	}
	for _, tc := range []struct {
		key  string
		want screen
	}{
		{"2", screenRoster},
		{"3", screenRun},
		{"4", screenHistory},
		{"1", screenProject},
	} {
		model = send(t, model, keyMsg(tc.key))
		if model.screen != tc.want {
			t.Fatalf("%q selected screen %v, want %v", tc.key, model.screen, tc.want)
		}
	}

	// The tabs work from the open unit card and land on the tree.
	model = selectRow(t, model, "unit:P001.1.2")
	model = send(t, model, keyMsg("enter"))
	if model.focus != focusDetails {
		t.Fatalf("focus after Enter = %v, want the card", model.focus)
	}
	model = send(t, model, keyMsg("3"))
	if model.screen != screenRun {
		t.Fatalf("3 from the card selected %v", model.screen)
	}
	model = send(t, model, keyMsg("1"))
	if model.screen != screenProject || model.focus != focusTree {
		t.Fatalf("1 from the stream selected %v with focus %v", model.screen, model.focus)
	}

	// An open help follows the screen switch instead of swallowing it.
	model = send(t, model, keyMsg("?"))
	model = send(t, model, keyMsg("2"))
	if !model.help || model.screen != screenRoster {
		t.Fatalf("help + 2 = help %v screen %v", model.help, model.screen)
	}
	if !strings.Contains(model.String(), "Keyboard and workspace · Ростер") {
		t.Fatalf("the help did not describe the roster screen:\n%s", model.String())
	}
}

// TestScreenTabsFitTheScreen keeps the tab row inside 32 columns and draws
// the full labels when there is room, with the run host state on the right.
func TestScreenTabsFitTheScreen(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root := writeTestProject(t)
	model := loadedModel(t, root)
	for _, size := range []struct{ w, h int }{{32, 10}, {120, 40}} {
		rendered := resize(t, model, size.w, size.h).String()
		assertScreenFits(t, rendered, size.w, size.h)
		lines := strings.Split(rendered, "\n")
		tabs := lines[1]
		if !strings.Contains(tabs, "[1 Проект]") || !strings.Contains(tabs, "run:idle") {
			t.Fatalf("%dx%d tab row = %q", size.w, size.h, tabs)
		}
		if size.w > 32 {
			for _, want := range []string{"2 Ростер", "3 Прогон", "4 История"} {
				if !strings.Contains(tabs, want) {
					t.Fatalf("%dx%d tab row lost %q: %q", size.w, size.h, want, tabs)
				}
			}
			continue
		}
		// A narrow terminal keeps the numbers; the active title stays too.
		for _, want := range []string{"2", "3", "4"} {
			if !strings.Contains(tabs, want) {
				t.Fatalf("%dx%d compact tab row lost %q: %q", size.w, size.h, want, tabs)
			}
		}
	}

	model = send(t, model, keyMsg("3"))
	rendered := resize(t, model, 120, 40).String()
	tabs := strings.Split(rendered, "\n")[1]
	if !strings.Contains(tabs, "[3 Прогон]") || strings.Contains(tabs, "[1 Проект]") {
		t.Fatalf("the active tab did not follow the screen: %q", tabs)
	}

	// The stream shows the host state, not a guessed one.
	model.runState = "Paused"
	tabs = strings.Split(resize(t, model, 120, 40).String(), "\n")[1]
	if !strings.Contains(tabs, "run:Paused") {
		t.Fatalf("tab row lost the run state: %q", tabs)
	}
}

// TestScreenKeysAreTextInSearch keeps 1-4 out of the screen switch while a
// search input owns the keyboard, on the tree and on the stream.
func TestScreenKeysAreTextInSearch(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	model = send(t, model, keyMsg("/"))
	for _, part := range []string{"1", "2", "3", "4"} {
		model = send(t, model, keyMsg(part))
	}
	if model.screen != screenProject {
		t.Fatalf("typing 1-4 left the project screen: %v", model.screen)
	}
	if model.query != "1234" {
		t.Fatalf("tree search query = %q, want 1234", model.query)
	}

	model = send(t, model, keyMsg("esc"))
	model = openStream(t, model)
	model = send(t, model, keyMsg("/"))
	for _, part := range []string{"1", "2"} {
		model = send(t, model, keyMsg(part))
	}
	if model.screen != screenRun || model.activityQuery != "12" {
		t.Fatalf("stream search: screen %v query %q", model.screen, model.activityQuery)
	}
}

// TestStartFromProjectOpensTheStream starts a run on the project screen and
// checks the workspace moves to the stream, while a refused start leaves it
// where the reader is.
func TestStartFromProjectOpensTheStream(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 120, 40)
	model = useControllers(t, model, newFakeController(runner.StateIdle))
	updated, _ := model.Update(keyMsg("s"))
	model = updated.(Model)
	if model.screen != screenRun {
		t.Fatalf("a start from the project selected screen %v, want the stream", model.screen)
	}
	if !strings.Contains(model.String(), "[3 Прогон]") {
		t.Fatalf("the stream tab is not active after the start:\n%s", model.String())
	}

	// Without a Queue plan nothing starts, so the screen stays put.
	empty := newModel(root, func() (Snapshot, error) { return Snapshot{Root: root}, nil }, true)
	empty.lockCheck = func(string) (int, bool, error) { return 0, false, nil }
	empty = resize(t, empty, 120, 40)
	empty = empty.applySnapshot(snapshotMsg{snapshot: Snapshot{Root: root}})
	empty = useControllers(t, empty, newFakeController(runner.StateIdle))
	updated, _ = empty.Update(keyMsg("s"))
	empty = updated.(Model)
	if empty.screen != screenProject {
		t.Fatalf("a refused start moved the workspace to %v", empty.screen)
	}
	if !strings.Contains(empty.notice, "nothing to start") {
		t.Fatalf("refused start notice = %q", empty.notice)
	}
}

// TestFooterShowsTheScreenKeys keeps the bottom line short: the keys of the
// current screen, never more than five, then the way to the help.
func TestFooterShowsTheScreenKeys(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 80, 24)
	footer := func() string {
		lines := strings.Split(model.String(), "\n")
		return strings.TrimRight(lines[len(lines)-1], " ")
	}
	project := footer()
	parts := strings.Split(project, " · ")
	if parts[len(parts)-1] != "? помощь" {
		t.Fatalf("project footer does not end with the help: %q", project)
	}
	if len(parts)-1 > 5 {
		t.Fatalf("project footer shows %d keys: %q", len(parts)-1, project)
	}
	for _, want := range []string{"Enter open", "s start"} {
		if !strings.Contains(project, want) {
			t.Fatalf("project footer lost %q: %q", want, project)
		}
	}

	model = send(t, model, keyMsg("3"))
	stream := footer()
	for _, want := range []string{"Enter expand", "[ ] page"} {
		if !strings.Contains(stream, want) {
			t.Fatalf("stream footer lost %q: %q", want, stream)
		}
	}
	if strings.Contains(stream, "s start") {
		t.Fatalf("stream footer repeats the project keys: %q", stream)
	}

	model = send(t, model, keyMsg("2"))
	if roster := footer(); roster != "? помощь" {
		t.Fatalf("placeholder footer = %q, want only the help", roster)
	}

	// A narrow footer keeps the help reachable: keys go, "? помощь" stays.
	narrow := resize(t, loadedModel(t, root), 32, 10)
	narrow = send(t, narrow, keyMsg("3"))
	lines := strings.Split(narrow.String(), "\n")
	last := strings.TrimRight(lines[len(lines)-1], " ")
	if !strings.HasSuffix(last, "? помощь") || lipgloss.Width(last) > 32 {
		t.Fatalf("32x10 stream footer = %q", last)
	}
}

// TestPlaceholderScreensSayWhatIsThere keeps the two screens without data
// honest: they name themselves, say the data is not there yet and point at
// where the work lives today.
func TestPlaceholderScreensSayWhatIsThere(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 80, 24)
	model = send(t, model, keyMsg("2"))
	if rendered := model.String(); !strings.Contains(rendered, "пока пусто") || !strings.Contains(rendered, "Ростер") || !strings.Contains(rendered, "docflow roster") {
		t.Fatalf("roster placeholder is not honest:\n%s", rendered)
	}
	model = send(t, model, keyMsg("4"))
	if rendered := model.String(); !strings.Contains(rendered, "пока пусто") || !strings.Contains(rendered, "История") {
		t.Fatalf("history placeholder is not honest:\n%s", rendered)
	}
}

// TestHelpBlocksSeparateCommonAndScreenKeys keeps the common keys in their own
// block, so each screen only documents what it adds.
func TestHelpBlocksSeparateCommonAndScreenKeys(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root := writeTestProject(t)
	model := resize(t, loadedModel(t, root), 100, 30)
	model = send(t, model, keyMsg("?"))
	rendered := model.String()
	lines := strings.Split(rendered, "\n")
	heading := func(text string) int {
		for i, line := range lines {
			if strings.TrimSpace(line) == text {
				return i
			}
		}
		return -1
	}
	common := heading("Any screen")
	project := heading("Проект")
	if common < 0 || project < 0 || project < common {
		t.Fatalf("help blocks are missing or out of order:\n%s", rendered)
	}
	block := strings.Join(lines[common:project], "\n")
	for _, want := range []string{"1–4", "q, Ctrl+C", "reload", "pause"} {
		if !strings.Contains(block, want) {
			t.Fatalf("common help block lost %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "Enter details") {
		t.Fatalf("common help block duplicates a screen key:\n%s", block)
	}
}
