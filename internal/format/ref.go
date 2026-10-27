package format

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"bsv-docflow/internal/clierrors"
)

// RefMissing is returned when a Read reference does not name a regular file.
type RefMissing struct{ clierrors.DocflowError }

// RefError is returned when a Read reference or its range is invalid.
type RefError struct{ clierrors.DocflowError }

// ReadRef is a parsed path, optional 1-based line range, or heading anchor.
type ReadRef struct {
	Path       string
	Start, End *int
	Anchor     string
}

var (
	readRefRE      = regexp.MustCompile(`^(.+?)(:L([0-9]+)-([0-9]+)|#(.+))?$`)
	markdownLinkRE = regexp.MustCompile(`^\[[^\]]*\]\([^)]*\)$`)
	headingLevelRE = regexp.MustCompile(`^(#{1,6})[ \t]+(.*?)[ \t#]*$`)
	// Go's regexp engine has no lookbehind. The prefix alternative below
	// implements the Python expression's allowed archive/ word boundary.
	ARCHIVE_LINK_RE = regexp.MustCompile(`(docs/|\.\./|^|[^\p{L}\p{N}_/.-])archive/(P\p{Nd}+-[\p{L}\p{N}_.-]*?\.md)`)
)

func newRefMissing(message string) RefMissing {
	return RefMissing{clierrors.DocflowError{Message: message, Code: clierrors.EXIT_FORMAT}}
}

func newRefError(message string) RefError {
	return RefError{clierrors.DocflowError{Message: message, Code: clierrors.EXIT_FORMAT}}
}

// ParseRef accepts path, path:Lstart-end, and path#anchor references.
// Surrounding backticks are removed to support plan Read items.
func ParseRef(text string) (ReadRef, error) {
	bare := strings.Trim(strings.TrimSpace(text), "`")
	if markdownLinkRE.MatchString(bare) {
		return ReadRef{}, newRefError(bare + ": markdown link: write a bare path from the project root (path, path:L1-9, path#anchor)")
	}
	match := readRefRE.FindStringSubmatch(bare)
	if len(match) == 0 {
		return ReadRef{}, newRefError("bad reference " + pythonRepr(text))
	}
	ref := ReadRef{Path: match[1], Anchor: match[5]}
	if match[3] != "" {
		start, err := strconv.Atoi(match[3])
		if err != nil {
			return ReadRef{}, newRefError("bad reference " + pythonRepr(text))
		}
		end, err := strconv.Atoi(match[4])
		if err != nil {
			return ReadRef{}, newRefError("bad reference " + pythonRepr(text))
		}
		ref.Start, ref.End = &start, &end
	}
	return ref, nil
}

// RefPath resolves a reference from the project root. A leading ~/ is
// expanded using the current user's home directory.
func RefPath(root string, ref ReadRef) (string, error) {
	if strings.HasPrefix(ref.Path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, filepath.FromSlash(ref.Path[2:])), nil
	}
	if filepath.IsAbs(ref.Path) {
		return filepath.Clean(ref.Path), nil
	}
	return filepath.Join(root, filepath.FromSlash(ref.Path)), nil
}

// HeadingSlug applies the anchor normalization used by GitHub headings.
func HeadingSlug(text string) string {
	var slug strings.Builder
	for _, char := range strings.ToLower(strings.TrimSpace(text)) {
		if unicode.IsLetter(char) || unicode.IsNumber(char) || char == ' ' || char == '-' || char == '_' {
			slug.WriteRune(char)
		}
	}
	return strings.ReplaceAll(slug.String(), " ", "-")
}

// AnchorRange returns the 0-based [start, stop) range for a heading section.
// Headings inside fenced code blocks are ignored.
func AnchorRange(lines []string, anchor string) (start, stop int, ok bool) {
	mask := FenceMask(lines)
	type heading struct {
		line  int
		level int
		text  string
	}
	headings := make([]heading, 0)
	for i, line := range lines {
		line = strings.TrimRight(line, "\r\n")
		match := headingLevelRE.FindStringSubmatch(line)
		if !mask[i] && len(match) != 0 {
			headings = append(headings, heading{line: i, level: len(match[1]), text: match[2]})
		}
	}
	counts := make(map[string]int)
	for i, current := range headings {
		slug := HeadingSlug(current.text)
		seen := counts[slug]
		counts[slug] = seen + 1
		if seen > 0 {
			slug = fmt.Sprintf("%s-%d", slug, seen)
		}
		if slug != anchor {
			continue
		}
		stop = len(lines)
		for _, next := range headings[i+1:] {
			if next.level <= current.level {
				stop = next.line
				break
			}
		}
		return current.line, stop, true
	}
	return 0, 0, false
}

func refLines(path string, ref ReadRef) ([]string, error) {
	text, err := readRefText(path)
	if err != nil {
		return nil, err
	}
	return refLinesFromText(text, ref)
}

func refStat(root string, ref ReadRef) (string, os.FileInfo, error) {
	path, err := RefPath(root, ref)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", nil, newRefMissing(fmt.Sprintf("%s: file not found", ref.Path))
	}
	return path, info, nil
}

// RefSize returns the UTF-8 byte count contributed by a Read reference.
func RefSize(root, text string) (int, error) {
	ref, err := ParseRef(text)
	if err != nil {
		return 0, err
	}
	path, info, err := refStat(root, ref)
	if err != nil {
		return 0, err
	}
	if ref.Start == nil && ref.Anchor == "" {
		return int(info.Size()), nil
	}
	lines, err := refLines(path, ref)
	if err != nil {
		return 0, err
	}
	size := 0
	for _, line := range lines {
		size += len(line)
	}
	return size, nil
}

// RefText returns the text contributed by a Read reference.
func RefText(root, text string) (string, error) {
	ref, err := ParseRef(text)
	if err != nil {
		return "", err
	}
	path, _, err := refStat(root, ref)
	if err != nil {
		return "", err
	}
	content, err := readRefText(path)
	if err != nil {
		return "", err
	}
	if ref.Start == nil && ref.Anchor == "" {
		return content, nil
	}
	lines, err := refLinesFromText(content, ref)
	if err != nil {
		return "", err
	}
	return strings.Join(lines, ""), nil
}

func refLinesFromText(text string, ref ReadRef) ([]string, error) {
	raw := SplitLines(text)
	if ref.Anchor != "" {
		lines := make([]string, len(raw))
		for i, line := range raw {
			lines[i] = strings.TrimRight(line, "\r\n")
		}
		start, stop, ok := AnchorRange(lines, ref.Anchor)
		if !ok {
			return nil, newRefError(fmt.Sprintf("%s: no anchor #%s", ref.Path, ref.Anchor))
		}
		return raw[start:stop], nil
	}
	if ref.Start == nil || ref.End == nil || *ref.Start < 1 || *ref.Start > *ref.End {
		start, end := 0, 0
		if ref.Start != nil {
			start = *ref.Start
		}
		if ref.End != nil {
			end = *ref.End
		}
		return nil, newRefError(fmt.Sprintf("%s: bad line range L%d-%d", ref.Path, start, end))
	}
	start, stop := *ref.Start-1, *ref.End
	if start >= len(raw) {
		return []string{}, nil
	}
	if stop > len(raw) {
		stop = len(raw)
	}
	return raw[start:stop], nil
}

func readRefText(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(content) {
		return "", fmt.Errorf("invalid UTF-8 in %s", path)
	}
	return string(content), nil
}

// ContextRefs returns the list items from the plan's Context section.
func ContextRefs(plan *Plan) []Item {
	if plan == nil {
		return []Item{}
	}
	section := plan.Section("Context")
	if section == nil {
		return []Item{}
	}
	lines := make([]string, len(plan.Raw))
	for i, line := range plan.Raw {
		lines[i] = strings.TrimRight(line, "\r\n")
	}
	return ParseItems(lines, FenceMask(lines), section.Line, section.End)
}

func rawPlanText(plan *Plan, start, end int) string {
	if start < 1 || start > len(plan.Raw) {
		return ""
	}
	if end > len(plan.Raw) {
		end = len(plan.Raw)
	}
	if end < start-1 {
		return ""
	}
	return strings.Join(plan.Raw[start-1:end], "")
}

// ArchiveGotchas returns active Gotchas items from archive entries linked by
// the plan's Context, Decisions, and the unit's Read references.
func ArchiveGotchas(root string, plan *Plan, unit *Unit) []string {
	if plan == nil || unit == nil {
		return []string{}
	}
	texts := make([]string, 0)
	for _, name := range []string{"Context", "Decisions"} {
		if section := plan.Section(name); section != nil {
			texts = append(texts, rawPlanText(plan, section.Line, section.End))
		}
	}
	if read := unit.Field("Read"); read != nil {
		for _, item := range read.Items {
			text, err := RefText(root, item.Text)
			if err == nil {
				texts = append(texts, text)
			}
		}
	}
	return archiveGotchas(root, texts)
}

// ArchiveGotchasForText returns active Gotchas items from archive entries
// linked inside a single text, e.g. one whole-file Read reference.
func ArchiveGotchasForText(root, text string) []string {
	return archiveGotchas(root, []string{text})
}

func archiveGotchas(root string, texts []string) []string {
	seen := make(map[string]bool)
	gotchas := make([]string, 0)
	for _, match := range ARCHIVE_LINK_RE.FindAllStringSubmatch(strings.Join(texts, "\n"), -1) {
		name := match[2]
		if !seen[name] {
			seen[name] = true
			gotchas = append(gotchas, name)
		}
	}
	out := make([]string, 0)
	for _, name := range gotchas {
		path := filepath.Join(root, "docs", "archive", name)
		content, err := readRefText(path)
		if err != nil {
			continue
		}
		entry, err := ParseArchive(content, path)
		if err != nil {
			continue
		}
		section := entry.Section("Gotchas")
		if _, superseded := entry.Meta["superseded_by"]; superseded || section == nil {
			continue
		}
		lines := make([]string, len(entry.Raw))
		for i, line := range entry.Raw {
			lines[i] = strings.TrimRight(line, "\r\n")
		}
		id := entry.Meta["id"]
		if id == "" {
			id = strings.SplitN(name, "-", 2)[0]
		}
		for _, item := range ParseItems(lines, FenceMask(lines), section.Line, section.End) {
			out = append(out, fmt.Sprintf("- (%s) %s", id, item.Text))
		}
	}
	return out
}
