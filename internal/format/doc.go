// Package format parses and edits docflow Markdown documents without changing
// their original line endings.
package format

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"bsv-docflow/internal/clierrors"
)

const DocflowFilePath = "docs/docflow.md"
const DocflowVersion = "3"

var UnitFields = []string{"Read", "Do", "Verify", "Traps", "Depends", "Executor", "Model", "Role", "Commit", "Handoff"}

var Statuses = map[string]string{" ": "todo", "~": "doing", "x": "done", "-": "skipped", "!": "blocked"}

var fenceRE = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})(.*)$")
var fieldRE = regexp.MustCompile("^([A-Z][A-Za-z-]*):(?:[ \\t]+(.*?))?[ \\t]*$")

type Item struct {
	Text string
	Line int
}

type Section struct {
	Name string
	Line int
	End  int
}

type Field struct {
	Key   string
	Value string
	Items []Item
	Line  int
	End   int
}

type Unit struct {
	ID     string
	Title  string
	Status string
	Line   int
	End    int
	Phase  *int
	Fields []Field
}

func (u *Unit) Field(key string) *Field {
	for i := range u.Fields {
		if u.Fields[i].Key == key {
			return &u.Fields[i]
		}
	}
	return nil
}

type Phase struct {
	Number int
	Title  string
	Line   int
	Units  []*Unit
}

type Doc struct {
	Path      string
	Raw       []string
	Meta      map[string]string
	MetaLines map[string]int
	Sections  []Section
}

func (d *Doc) Section(name string) *Section {
	for i := range d.Sections {
		if d.Sections[i].Name == name {
			return &d.Sections[i]
		}
	}
	return nil
}

func (d *Doc) Text() string { return strings.Join(d.Raw, "") }

func (d *Doc) SetMeta(key, value string) error {
	line, ok := d.MetaLines[key]
	if !ok {
		return docflowError(fmt.Sprintf("%s: no %s in frontmatter", d.Path, pythonRepr(key)))
	}
	old := d.Raw[line-1]
	trimmed := strings.TrimRight(old, "\r\n")
	d.Raw[line-1] = key + ": " + value + old[len(trimmed):]
	return d.reparseCommon()
}

func (d *Doc) RemoveLine(line int) error {
	if err := d.removeRaw(line); err != nil {
		return err
	}
	return d.reparseCommon()
}

// InsertMeta inserts key: value after the given 1-based line; after <= 0
// appends at the end of the frontmatter, before the closing marker.
func (d *Doc) InsertMeta(after int, key, value string) error {
	line := after
	if line < 1 {
		lines := make([]string, len(d.Raw))
		for i, raw := range d.Raw {
			lines[i] = strings.TrimRight(raw, "\r\n")
		}
		_, _, body, err := ParseMeta(d.Path, lines)
		if err != nil {
			return err
		}
		line = body - 1
	}
	if err := d.insertRaw(line, []string{key + ": " + value}); err != nil {
		return err
	}
	return d.reparseCommon()
}

func (d *Doc) removeRaw(line int) error {
	if line < 1 || line > len(d.Raw) {
		return fmt.Errorf("line %d out of range", line)
	}
	d.Raw = append(d.Raw[:line-1], d.Raw[line:]...)
	return nil
}

func (d *Doc) AppendSectionItems(name string, items []string) error {
	section := d.Section(name)
	if section == nil {
		return docflowError(fmt.Sprintf("%s: no section %s", d.Path, pythonRepr(name)))
	}
	after := section.Line
	for line := section.Line + 1; line <= section.End; line++ {
		if line-1 < len(d.Raw) && strings.TrimSpace(d.Raw[line-1]) != "" {
			after = line
		}
	}
	lines := make([]string, len(items))
	for i, item := range items {
		lines[i] = "- " + item
	}
	if err := d.insertRaw(after, lines); err != nil {
		return err
	}
	return d.reparseCommon()
}

func (d *Doc) Save(path string) error {
	if path == "" {
		path = d.Path
	}
	return os.WriteFile(path, []byte(d.Text()), 0o666)
}

func (d *Doc) newline() string {
	if len(d.Raw) > 0 && strings.HasSuffix(d.Raw[0], "\r\n") {
		return "\r\n"
	}
	return "\n"
}

func (d *Doc) insertRaw(after int, lines []string) error {
	if after < 0 || after > len(d.Raw) {
		return fmt.Errorf("line %d out of range", after)
	}
	nl := d.newline()
	if after > 0 && !strings.HasSuffix(d.Raw[after-1], "\n") {
		d.Raw[after-1] += nl
	}
	insert := make([]string, len(lines))
	for i, line := range lines {
		insert[i] = line + nl
	}
	d.Raw = append(d.Raw[:after], append(insert, d.Raw[after:]...)...)
	return nil
}

func (d *Doc) reparseCommon() error {
	fresh, err := parseDoc(d.Text(), d.Path)
	if err != nil {
		return err
	}
	*d = *fresh
	return nil
}

type DocflowFile struct {
	Doc
	Queue   []Item
	Frozen  []Item
	Backlog []Item
}

func (d *DocflowFile) AddFrozen(text string) error {
	if d.Section("Frozen") != nil {
		return d.AppendSectionItems("Frozen", []string{text})
	}
	backlog := d.Section("Backlog")
	if backlog != nil {
		if err := d.insertRaw(backlog.Line-1, []string{"## Frozen", "- " + text, ""}); err != nil {
			return err
		}
	} else if err := d.insertRaw(len(d.Raw), []string{"", "## Frozen", "- " + text}); err != nil {
		return err
	}
	return d.reparseDocflow()
}

func (d *DocflowFile) AppendSectionItems(name string, items []string) error {
	section := d.Section(name)
	if section == nil {
		return docflowError(fmt.Sprintf("%s: no section %s", d.Path, pythonRepr(name)))
	}
	after := section.Line
	for line := section.Line + 1; line <= section.End; line++ {
		if line-1 < len(d.Raw) && strings.TrimSpace(d.Raw[line-1]) != "" {
			after = line
		}
	}
	newLines := make([]string, len(items))
	for i, item := range items {
		newLines[i] = "- " + item
	}
	if err := d.insertRaw(after, newLines); err != nil {
		return err
	}
	return d.reparseDocflow()
}

func (d *DocflowFile) RemoveLine(line int) error {
	if err := d.removeRaw(line); err != nil {
		return err
	}
	return d.reparseDocflow()
}

func (d *DocflowFile) reparseDocflow() error {
	fresh, err := ParseDocflow(d.Text(), d.Path)
	if err != nil {
		return err
	}
	*d = *fresh
	return nil
}

type Archive struct{ Doc }

type Plan struct {
	Doc
	Phases []*Phase
	Units  []*Unit
}

func (p *Plan) Unit(id string) *Unit {
	for _, unit := range p.Units {
		if unit.ID == id {
			return unit
		}
	}
	return nil
}

func (p *Plan) SetStatus(id, status string) error {
	if _, ok := Statuses[status]; !ok {
		return fmt.Errorf("unknown status %s", pythonRepr(status))
	}
	unit := p.Unit(id)
	if unit == nil {
		return docflowError(fmt.Sprintf("%s: no unit %s", p.Path, id))
	}
	line := p.Raw[unit.Line-1]
	if !strings.HasPrefix(line, "### [") {
		return fmt.Errorf("unit heading at line %d is malformed", unit.Line)
	}
	close := strings.IndexByte(line[len("### ["):], ']')
	if close < 0 {
		return fmt.Errorf("unit heading at line %d is malformed", unit.Line)
	}
	close += len("### [")
	p.Raw[unit.Line-1] = "### [" + status + "]" + line[close+1:]
	return p.reparsePlan()
}

func (p *Plan) SetField(id, key, value string, items []string) error {
	order := fieldIndex(key)
	if order < 0 {
		return fmt.Errorf("unknown field %s", pythonRepr(key))
	}
	unit := p.Unit(id)
	if unit == nil {
		return docflowError(fmt.Sprintf("%s: no unit %s", p.Path, id))
	}
	newLines := []string{key + ":"}
	if value != "" {
		newLines[0] = key + ": " + value
	}
	for _, item := range items {
		newLines = append(newLines, "- "+item)
	}
	if old := unit.Field(key); old != nil {
		nl := p.newline()
		replacement := make([]string, len(newLines))
		for i, line := range newLines {
			replacement[i] = line + nl
		}
		p.Raw = append(p.Raw[:old.Line-1], append(replacement, p.Raw[old.End:]...)...)
		return p.reparsePlan()
	}
	insertAfter := unit.Line
	for _, field := range unit.Fields {
		fieldOrder := fieldIndex(field.Key)
		if fieldOrder >= 0 && fieldOrder < order && field.End > insertAfter {
			insertAfter = field.End
		}
	}
	if err := p.insertRaw(insertAfter, newLines); err != nil {
		return err
	}
	return p.reparsePlan()
}

func (p *Plan) AppendItems(id, key string, items []string) error {
	unit := p.Unit(id)
	if unit == nil {
		return docflowError(fmt.Sprintf("%s: no unit %s", p.Path, id))
	}
	field := unit.Field(key)
	if field == nil {
		return p.SetField(id, key, "", items)
	}
	lines := make([]string, len(items))
	for i, item := range items {
		lines[i] = "- " + item
	}
	if err := p.insertRaw(field.End, lines); err != nil {
		return err
	}
	return p.reparsePlan()
}

func (p *Plan) AppendSectionItems(name string, items []string) error {
	section := p.Section(name)
	if section == nil {
		return docflowError(fmt.Sprintf("%s: no section %s", p.Path, pythonRepr(name)))
	}
	after := section.Line
	for line := section.Line + 1; line <= section.End; line++ {
		if line-1 < len(p.Raw) && strings.TrimSpace(p.Raw[line-1]) != "" {
			after = line
		}
	}
	newLines := make([]string, len(items))
	for i, item := range items {
		newLines[i] = "- " + item
	}
	if err := p.insertRaw(after, newLines); err != nil {
		return err
	}
	return p.reparsePlan()
}

func (p *Plan) RemoveLine(line int) error {
	if err := p.removeRaw(line); err != nil {
		return err
	}
	return p.reparsePlan()
}

func (p *Plan) reparsePlan() error {
	fresh, err := ParsePlan(p.Text(), p.Path)
	if err != nil {
		return err
	}
	*p = *fresh
	return nil
}

func fieldIndex(key string) int {
	for i, candidate := range UnitFields {
		if candidate == key {
			return i
		}
	}
	return -1
}

func SplitLines(text string) []string {
	parts := strings.Split(text, "\n")
	lines := make([]string, 0, len(parts))
	for _, part := range parts[:len(parts)-1] {
		lines = append(lines, part+"\n")
	}
	if parts[len(parts)-1] != "" {
		lines = append(lines, parts[len(parts)-1])
	}
	return lines
}

func FenceMask(lines []string) []bool {
	mask := make([]bool, len(lines))
	var fence string
	for i, line := range lines {
		match := fenceRE.FindStringSubmatch(line)
		if fence == "" {
			if len(match) == 0 || (match[1][0] == '`' && strings.Contains(match[2], "`")) {
				continue
			}
			fence = match[1]
			mask[i] = true
			continue
		}
		mask[i] = true
		if len(match) > 0 && match[1][0] == fence[0] && len(match[1]) >= len(fence) && strings.TrimSpace(match[2]) == "" {
			fence = ""
		}
	}
	return mask
}

func ParseMeta(path string, lines []string) (map[string]string, map[string]int, int, error) {
	if len(lines) == 0 || lines[0] != "---" {
		return nil, nil, 0, &clierrors.FormatError{Path: path, Line: 1, Text: "missing frontmatter"}
	}
	meta := make(map[string]string)
	metaLines := make(map[string]int)
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			return meta, metaLines, i + 1, nil
		}
		key, value, ok := parseMetaLine(lines[i])
		if !ok {
			return nil, nil, 0, &clierrors.FormatError{Path: path, Line: i + 1, Text: "bad frontmatter line: " + pythonRepr(lines[i])}
		}
		if _, exists := meta[key]; exists {
			return nil, nil, 0, &clierrors.FormatError{Path: path, Line: i + 1, Text: "duplicate frontmatter key " + pythonRepr(key)}
		}
		meta[key] = value
		metaLines[key] = i + 1
	}
	return nil, nil, 0, &clierrors.FormatError{Path: path, Line: 1, Text: "unterminated frontmatter"}
}

func parseMetaLine(line string) (string, string, bool) {
	colon := strings.IndexByte(line, ':')
	if colon < 1 {
		return "", "", false
	}
	key := line[:colon]
	first, firstSize := utf8.DecodeRuneInString(key)
	if firstSize == 0 || !((first >= 'A' && first <= 'Z') || (first >= 'a' && first <= 'z') || first == '_') {
		return "", "", false
	}
	for _, r := range key[firstSize:] {
		if r != '_' && r != '-' && !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			return "", "", false
		}
	}
	return key, strings.Trim(line[colon+1:], " \t"), true
}

func parseDoc(text, path string) (*Doc, error) {
	raw := SplitLines(text)
	lines := make([]string, len(raw))
	for i, line := range raw {
		lines[i] = strings.TrimRight(line, "\r\n")
	}
	mask := FenceMask(lines)
	meta, metaLines, body, err := ParseMeta(path, lines)
	if err != nil {
		return nil, err
	}
	sections := make([]Section, 0)
	for i := body; i < len(lines); i++ {
		if mask[i] {
			continue
		}
		name, ok := h2Heading(lines[i])
		if !ok {
			continue
		}
		if len(sections) > 0 {
			sections[len(sections)-1].End = i
		}
		sections = append(sections, Section{Name: stripTerminalComment(name), Line: i + 1, End: len(lines)})
	}
	return &Doc{Path: path, Raw: raw, Meta: meta, MetaLines: metaLines, Sections: sections}, nil
}

func ParseItems(lines []string, mask []bool, start, stop int) []Item {
	items := make([]Item, 0)
	var current *Item
	for i := start; i < stop && i < len(lines); i++ {
		if i < 0 {
			continue
		}
		line := lines[i]
		if i < len(mask) && mask[i] {
			current = nil
			continue
		}
		if text, ok := itemLine(line); ok {
			items = append(items, Item{Text: text, Line: i + 1})
			current = &items[len(items)-1]
		} else if current != nil && continuationLine(line) {
			current.Text += " " + strings.TrimSpace(line)
		}
	}
	return items
}

func ParseFields(lines []string, mask []bool, start, stop int) []Field {
	fields := make([]Field, 0)
	current := -1
	for i := start; i < stop && i < len(lines); i++ {
		if i < 0 {
			continue
		}
		line := lines[i]
		if (i < len(mask) && mask[i]) || strings.TrimSpace(line) == "" {
			current = -1
			continue
		}
		if current >= 0 {
			field := &fields[current]
			if text, ok := itemLine(line); ok {
				field.Items = append(field.Items, Item{Text: text, Line: i + 1})
				field.End = i + 1
				continue
			}
			if len(field.Items) > 0 && continuationLine(line) {
				field.Items[len(field.Items)-1].Text += " " + strings.TrimSpace(line)
				field.End = i + 1
				continue
			}
			if field.Value != "" && len(field.Items) == 0 && !fieldLine(line) {
				field.Value += " " + strings.TrimSpace(line)
				field.End = i + 1
				continue
			}
			current = -1
		}
		if key, value, ok := fieldParts(line); ok {
			fields = append(fields, Field{Key: key, Value: value, Items: []Item{}, Line: i + 1, End: i + 1})
			current = len(fields) - 1
		}
	}
	return fields
}

func ParsePlan(text, path string) (*Plan, error) {
	doc, lines, mask, err := parseDocument(text, path)
	if err != nil {
		return nil, err
	}
	body := len(lines)
	if len(doc.Sections) > 0 {
		body = doc.Sections[0].Line - 1
	}
	type heading struct {
		line  string
		index int
	}
	heads := make([]heading, 0)
	for i := body; i < len(lines); i++ {
		if !mask[i] && headingStart(lines[i]) {
			heads = append(heads, heading{line: lines[i], index: i})
		}
	}
	phases := make([]*Phase, 0)
	units := make([]*Unit, 0)
	var phase *Phase
	for n, head := range heads {
		stop := len(lines)
		if n+1 < len(heads) {
			stop = heads[n+1].index
		}
		if title, ok := h2Raw(head.line); ok {
			if number, name, ok := phaseHeading(title); ok {
				phase = &Phase{Number: number, Title: name, Line: head.index + 1, Units: []*Unit{}}
				phases = append(phases, phase)
			} else {
				phase = nil
			}
			continue
		}
		id, title, status, ok := unitHeading(head.line)
		if !ok {
			continue
		}
		end := head.index + 1
		for j := head.index + 1; j < stop; j++ {
			if strings.TrimSpace(lines[j]) != "" {
				end = j + 1
			}
		}
		unit := &Unit{ID: id, Title: title, Status: status, Line: head.index + 1, End: end, Fields: ParseFields(lines, mask, head.index+1, stop)}
		if phase != nil {
			number := phase.Number
			unit.Phase = &number
			phase.Units = append(phase.Units, unit)
		}
		units = append(units, unit)
	}
	return &Plan{Doc: *doc, Phases: phases, Units: units}, nil
}

func ParseDocflow(text, path string) (*DocflowFile, error) {
	doc, lines, mask, err := parseDocument(text, path)
	if err != nil {
		return nil, err
	}
	result := &DocflowFile{Doc: *doc, Queue: []Item{}, Frozen: []Item{}, Backlog: []Item{}}
	for _, section := range doc.Sections {
		items := ParseItems(lines, mask, section.Line, section.End)
		switch section.Name {
		case "Queue":
			result.Queue = items
		case "Frozen":
			result.Frozen = items
		case "Backlog":
			result.Backlog = items
		}
	}
	return result, nil
}

func ParseArchive(text, path string) (*Archive, error) {
	doc, err := parseDoc(text, path)
	if err != nil {
		return nil, err
	}
	return &Archive{Doc: *doc}, nil
}

func ReadText(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// CheckVersion reports the format error ordinary CLI commands return before
// operating on a project.
func CheckVersion(root string) error {
	text, err := ReadText(filepath.Join(root, filepath.FromSlash(DocflowFilePath)))
	if err != nil {
		return err
	}
	doc, err := ParseDocflow(text, DocflowFilePath)
	if err != nil {
		return err
	}
	version := doc.Meta["docflow"]
	if version == DocflowVersion {
		return nil
	}
	line := doc.MetaLines["docflow"]
	if line == 0 {
		line = 1
	}
	found := "no version"
	if version != "" {
		found = "version " + version
	}
	return &clierrors.FormatError{
		Path: DocflowFilePath, Line: line,
		Text: fmt.Sprintf("format %s, expected %s; run docflow migrate", found, DocflowVersion),
	}
}

func LoadPlan(path string) (*Plan, error) {
	text, err := ReadText(path)
	if err != nil {
		return nil, err
	}
	return ParsePlan(text, path)
}

func LoadDocflow(path string) (*DocflowFile, error) {
	text, err := ReadText(path)
	if err != nil {
		return nil, err
	}
	return ParseDocflow(text, path)
}

func LoadArchive(path string) (*Archive, error) {
	text, err := ReadText(path)
	if err != nil {
		return nil, err
	}
	return ParseArchive(text, path)
}

func FindRoot(start string) (string, error) {
	resolved, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	if real, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil {
		resolved = real
	}
	resolved = filepath.Clean(resolved)
	for dir := resolved; ; dir = filepath.Dir(dir) {
		info, statErr := os.Stat(filepath.Join(dir, filepath.FromSlash(DocflowFilePath)))
		if statErr == nil && info.Mode().IsRegular() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return "", docflowError(fmt.Sprintf("%s not found in %s or above; run docflow init", DocflowFilePath, resolved))
}

func UnitDepends(plan *Plan, unit *Unit) ([]string, error) {
	if field := unit.Field("Depends"); field != nil {
		value := strings.TrimSpace(field.Value)
		if value == "" || value == "-" {
			return []string{}, nil
		}
		parts := strings.Split(value, ",")
		depends := make([]string, 0, len(parts))
		for _, part := range parts {
			if value := strings.TrimSpace(part); value != "" {
				depends = append(depends, value)
			}
		}
		return depends, nil
	}
	for i, candidate := range plan.Units {
		if candidate == unit || candidate.ID == unit.ID {
			if i > 0 {
				return []string{plan.Units[i-1].ID}, nil
			}
			return []string{}, nil
		}
	}
	return nil, docflowError("unit is not in plan")
}

type Prerequisite struct {
	Text    string
	Command *string
	Once    bool
}

func PlanPermissions(plan *Plan) string {
	if plan.Meta["permissions"] == "full" {
		return "full"
	}
	return "auto"
}

func PlanStarted(plan *Plan) bool {
	for _, unit := range plan.Units {
		if unit.Status != " " && unit.Status != "-" {
			return true
		}
	}
	return false
}

func PlanPrerequisites(plan *Plan) []Prerequisite {
	section := plan.Section("Prerequisites")
	if section == nil {
		return []Prerequisite{}
	}
	lines := make([]string, len(plan.Raw))
	for i, line := range plan.Raw {
		lines[i] = strings.TrimRight(line, "\r\n")
	}
	mask := FenceMask(lines)
	items := ParseItems(lines, mask, section.Line, section.End)
	out := make([]Prerequisite, 0, len(items))
	for _, item := range items {
		text := strings.TrimSpace(item.Text)
		once := strings.HasSuffix(text, " (once)")
		if once {
			text = strings.TrimSuffix(text, " (once)")
		}
		if label, command, ok := prerequisiteParts(text); ok {
			out = append(out, Prerequisite{Text: label, Command: &command, Once: once})
		} else {
			out = append(out, Prerequisite{Text: text, Once: once})
		}
	}
	return out
}

func prerequisiteParts(text string) (string, string, bool) {
	for i := len(text) - 1; i >= 0; i-- {
		if text[i] != ':' {
			continue
		}
		tail := strings.TrimLeftFunc(text[i+1:], unicode.IsSpace)
		if len(tail) < 2 || tail[0] != '`' || tail[len(tail)-1] != '`' {
			continue
		}
		command := tail[1 : len(tail)-1]
		if strings.Contains(command, "`") {
			continue
		}
		return strings.TrimSpace(text[:i]), command, true
	}
	return "", "", false
}

func parseDocument(text, path string) (*Doc, []string, []bool, error) {
	doc, err := parseDoc(text, path)
	if err != nil {
		return nil, nil, nil, err
	}
	lines := make([]string, len(doc.Raw))
	for i, line := range doc.Raw {
		lines[i] = strings.TrimRight(line, "\r\n")
	}
	return doc, lines, FenceMask(lines), nil
}

func h2Heading(line string) (string, bool) {
	return h2Raw(line)
}

func h2Raw(line string) (string, bool) {
	if !strings.HasPrefix(line, "## ") || len(line) == len("## ") {
		return "", false
	}
	return trimRightSpace(line[len("## "):]), true
}

func stripTerminalComment(value string) string {
	start := strings.LastIndex(value, "<!--")
	if start < 0 {
		return value
	}
	end := strings.Index(value[start+4:], "-->")
	if end < 0 {
		return value
	}
	end += start + 4
	if strings.TrimSpace(value[end+3:]) != "" {
		return value
	}
	for start > 0 {
		r, size := utf8.DecodeLastRuneInString(value[:start])
		if !unicode.IsSpace(r) {
			break
		}
		start -= size
	}
	return value[:start]
}

func headingStart(line string) bool {
	count := 0
	for _, r := range line {
		if r != '#' {
			return count > 0 && unicode.IsSpace(r)
		}
		count++
		if count > 6 {
			return false
		}
	}
	return count > 0 && count <= 6
}

func phaseHeading(title string) (int, string, bool) {
	if !strings.HasPrefix(title, "Phase ") {
		return 0, "", false
	}
	start := len("Phase ")
	end, ok := scanDigits(title, start)
	if !ok || !strings.HasPrefix(title[end:], ": ") || len(title[end+2:]) == 0 {
		return 0, "", false
	}
	number, ok := decimalNumber(title[start:end])
	if !ok {
		return 0, "", false
	}
	return number, title[end+2:], true
}

func unitHeading(line string) (id, title, status string, ok bool) {
	const prefix = "### ["
	if !strings.HasPrefix(line, prefix) {
		return "", "", "", false
	}
	_, size := utf8.DecodeRuneInString(line[len(prefix):])
	if size == 0 {
		return "", "", "", false
	}
	close := len(prefix) + size
	status = line[len(prefix):close]
	if !strings.HasPrefix(line[close:], "] ") {
		return "", "", "", false
	}
	start := close + 2
	if start >= len(line) || line[start] != 'P' {
		return "", "", "", false
	}
	position := start + 1
	for segment := 0; segment < 3; segment++ {
		var found bool
		position, found = scanDigits(line, position)
		if !found {
			return "", "", "", false
		}
		if segment < 2 {
			if position >= len(line) || line[position] != '.' {
				return "", "", "", false
			}
			position++
		}
	}
	if position >= len(line) || line[position] != ' ' || position+1 >= len(line) {
		return "", "", "", false
	}
	id = line[start:position]
	title = trimRightSpace(line[position+1:])
	return id, title, status, true
}

func scanDigits(value string, start int) (int, bool) {
	position := start
	for position < len(value) {
		r, size := utf8.DecodeRuneInString(value[position:])
		if !unicode.IsDigit(r) {
			break
		}
		position += size
	}
	return position, position > start
}

func decimalNumber(value string) (int, bool) {
	number := 0
	for _, r := range value {
		digit, ok := decimalDigit(r)
		if !ok || number > (int(^uint(0)>>1)-digit)/10 {
			return 0, false
		}
		number = number*10 + digit
	}
	return number, true
}

func decimalDigit(r rune) (int, bool) {
	if r >= '0' && r <= '9' {
		return int(r - '0'), true
	}
	for _, block := range unicode.Nd.R16 {
		if r >= rune(block.Lo) && r <= rune(block.Hi) && (uint16(r)-block.Lo)%block.Stride == 0 {
			return int((uint16(r)-block.Lo)/block.Stride) % 10, true
		}
	}
	for _, block := range unicode.Nd.R32 {
		if uint32(r) >= block.Lo && uint32(r) <= block.Hi && (uint32(r)-block.Lo)%block.Stride == 0 {
			return int((uint32(r)-block.Lo)/block.Stride) % 10, true
		}
	}
	return 0, false
}

func itemLine(line string) (string, bool) {
	if !strings.HasPrefix(line, "- ") {
		return "", false
	}
	return trimRightSpace(line[2:]), true
}

func continuationLine(line string) bool {
	position := 0
	for position < len(line) && (line[position] == ' ' || line[position] == '\t') {
		position++
	}
	if position == 0 || position == len(line) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(line[position:])
	return !unicode.IsSpace(r)
}

func fieldLine(line string) bool {
	_, _, ok := fieldParts(line)
	return ok
}

func fieldParts(line string) (string, string, bool) {
	match := fieldRE.FindStringSubmatch(line)
	if len(match) == 0 {
		return "", "", false
	}
	return match[1], match[2], true
}

func trimRightSpace(value string) string {
	return strings.TrimRightFunc(value, unicode.IsSpace)
}

func docflowError(message string) error {
	return clierrors.DocflowError{Message: message, Code: clierrors.EXIT_FORMAT}
}

func pythonRepr(value string) string {
	if strings.Contains(value, "'") && !strings.Contains(value, `"`) {
		return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
	}
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `'`, `\'`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, "\r", `\r`)
	value = strings.ReplaceAll(value, "\t", `\t`)
	return "'" + value + "'"
}
