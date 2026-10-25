package roster

import (
	"sort"
	"strings"
)

// lineIndex maps "table|key" paths of a roster file to 1-based line
// numbers, so validation errors can name the line they come from.
// lint.ParseTOML returns plain maps without positions; this index restores
// them with a line scan of the same restricted TOML subset (one [table]
// header per line, one key = value per line, # comments skipped).
type lineIndex struct {
	lines map[string]int
}

// headerSegments splits a table header [a."b".c] into its segments:
// the dot separates only outside quotes, and every segment drops its
// quotes and surrounding spaces, so the index keys match the keys of
// the agents.ParseTOML maps.
func headerSegments(header string) []string {
	var segments []string
	var current strings.Builder
	quoted := byte(0)
	flush := func() {
		segment := strings.Trim(strings.TrimSpace(current.String()), `"'`)
		if segment != "" {
			segments = append(segments, segment)
		}
		current.Reset()
	}
	for i := 0; i < len(header); i++ {
		c := header[i]
		switch {
		case quoted != 0:
			if c == quoted {
				quoted = 0
			}
			current.WriteByte(c)
		case c == '"' || c == '\'':
			quoted = c
			current.WriteByte(c)
		case c == '.':
			flush()
		default:
			current.WriteByte(c)
		}
	}
	flush()
	return segments
}

func scanLines(text string) *lineIndex {
	index := &lineIndex{lines: make(map[string]int)}
	table := ""
	for n, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if end := strings.Index(line, "]"); end > 0 {
				segments := headerSegments(line[1:end])
				table = strings.Join(segments, ".")
				index.set(table, "", n+1)
				// A dotted header [a.b.c] also answers lookups of each
				// segment as a key of its parent table.
				for i := 1; i < len(segments); i++ {
					index.set(strings.Join(segments[:i], "."), segments[i], n+1)
				}
			}
			continue
		}
		key, _, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.Trim(strings.TrimSpace(key), `"'`)
		if key == "" {
			continue
		}
		index.set(table, key, n+1)
		// A dotted key also answers lookups for its table prefixes.
		if strings.Contains(key, ".") {
			parts := strings.Split(key, ".")
			for i := 1; i < len(parts); i++ {
				index.set(table, strings.Join(parts[:i], "."), n+1)
			}
		}
	}
	return index
}

func (index *lineIndex) set(table, key string, line int) {
	entry := table + "|" + key
	if _, ok := index.lines[entry]; !ok {
		index.lines[entry] = line
	}
}

// line returns the 1-based line of a key inside a table, or 0 when the
// scan has no entry (exotic syntax); callers fall back to the file head.
func (index *lineIndex) line(table, key string) int {
	return index.lines[table+"|"+key]
}

// keysByLine sorts the keys of a table by their line in the file, so
// validation errors come out in file order; names without a scan entry
// sort last, alphabetically.
func (index *lineIndex) keysByLine(table string, values map[string]any) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sortByLine(index, table, names)
	return names
}

func sortByLine(index *lineIndex, table string, names []string) {
	lines := make(map[string]int, len(names))
	for _, name := range names {
		lines[name] = index.line(table, name)
	}
	sort.Slice(names, func(i, j int) bool {
		li, lj := lines[names[i]], lines[names[j]]
		if li != lj {
			if li == 0 {
				return false
			}
			if lj == 0 {
				return true
			}
			return li < lj
		}
		return names[i] < names[j]
	})
}
