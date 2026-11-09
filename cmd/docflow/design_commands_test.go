package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The command list lives in `commands` (and the roster subcommands in
// `rosterCommands`); the design tables and every doc that names a command
// are copies of it. These tests compare sets of names, not text: a command
// added, removed or renamed on one side fails them, rewording a row or
// reordering a table does not. The order of the root help is pinned
// separately by TestRootHelpListsRegisteredCommands.

var docCodeSpan = regexp.MustCompile("`([^`\n]+)`")

// repoFile reads a file by its path from the repository root.
func repoFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// designTableSpans returns the code spans of the first column of the tables
// of the `## heading` section of a design topic.
func designTableSpans(t *testing.T, path, heading string) [][]string {
	t.Helper()
	text := repoFile(t, path)
	start := strings.Index(text, "\n## "+heading+"\n")
	if start < 0 {
		t.Fatalf("%s: no section ## %s", path, heading)
	}
	body := text[start+1:]
	if end := strings.Index(body[3:], "\n## "); end >= 0 {
		body = body[:end+3]
	}
	var rows [][]string
	for _, line := range strings.Split(body, "\n") {
		// An escaped pipe belongs to the cell (`--needs human\|plan`).
		cells := strings.Split(strings.ReplaceAll(line, `\|`, "/"), "|")
		if len(cells) < 3 || !strings.HasPrefix(line, "|") {
			continue
		}
		var spans []string
		for _, m := range docCodeSpan.FindAllStringSubmatch(cells[1], -1) {
			spans = append(spans, m[1])
		}
		if len(spans) > 0 {
			rows = append(rows, spans)
		}
	}
	return rows
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// compareNames reports the names present on one side only.
func compareNames(t *testing.T, what string, registered, documented map[string]bool) {
	t.Helper()
	only := func(a, b map[string]bool) []string {
		var out []string
		for _, key := range sortedKeys(a) {
			if !b[key] {
				out = append(out, key)
			}
		}
		return out
	}
	if missing := only(registered, documented); len(missing) > 0 {
		t.Errorf("%s: registered but not documented: %q", what, missing)
	}
	if extra := only(documented, registered); len(extra) > 0 {
		t.Errorf("%s: documented but not registered: %q", what, extra)
	}
}

func visibleCommands() map[string]bool {
	set := map[string]bool{}
	for _, spec := range commands {
		if !spec.hidden {
			set[spec.name] = true
		}
	}
	return set
}

// TestCommandsMatchDesign: the command table of docs/design/cli.md names
// every visible registered command and nothing else. A cell names its
// commands by the first word of each code span (`prompt`, `verify`, ...);
// `--version` is a flag, not a command.
func TestCommandsMatchDesign(t *testing.T) {
	documented := map[string]bool{}
	for _, spans := range designTableSpans(t, "docs/design/cli.md", "CLI docflow") {
		for _, span := range spans {
			word := strings.Fields(span)[0]
			if !strings.HasPrefix(word, "-") {
				documented[word] = true
			}
		}
	}
	compareNames(t, "docs/design/cli.md commands", visibleCommands(), documented)
}

// TestRosterCommandsMatchDesign: the command table of docs/design/roster.md
// names every roster subcommand the dispatch knows and nothing else.
func TestRosterCommandsMatchDesign(t *testing.T) {
	documented := map[string]bool{}
	for _, spans := range designTableSpans(t, "docs/design/roster.md", "Команды") {
		for _, span := range spans {
			words := strings.Fields(span)
			if words[0] == "roster" && len(words) > 1 {
				words = words[1:]
			}
			documented[words[0]] = true
		}
	}
	registered := map[string]bool{}
	for _, name := range rosterCommandNames() {
		registered[name] = true
	}
	compareNames(t, "docs/design/roster.md roster subcommands", registered, documented)
}

// docCommandMention matches `docflow <command> [<subcommand>]` at the start
// of a code span or a fenced line; docflow-dev is the same CLI.
var docCommandMention = regexp.MustCompile(`^docflow(?:-dev)? ([a-z][a-z-]*)(?: ([a-z][a-z-]*))?`)

// TestDocsNameRegisteredCommands: every `docflow X` the user-facing docs and
// the skills show as code — an inline span or a line of a fenced block —
// names a registered command, and `docflow roster Y` a roster subcommand.
// A removed or renamed command fails here instead of staying in a skill.
// The archive is history and is not checked.
func TestDocsNameRegisteredCommands(t *testing.T) {
	registered := map[string]bool{}
	for _, spec := range commands {
		registered[spec.name] = true
	}
	roster := map[string]bool{}
	for _, name := range rosterCommandNames() {
		roster[name] = true
	}
	paths := []string{"README.md", "README.ru.md", "AGENTS.md", "docs/usage.md"}
	for _, pattern := range []string{"docs/design/*.md", "skills/*/SKILL.md"} {
		matches, err := filepath.Glob(filepath.Join("..", "..", filepath.FromSlash(pattern)))
		if err != nil || len(matches) == 0 {
			t.Fatalf("%s: %v", pattern, err)
		}
		for _, match := range matches {
			rel, _ := filepath.Rel(filepath.Join("..", ".."), match)
			paths = append(paths, filepath.ToSlash(rel))
		}
	}
	checked := 0
	for _, path := range paths {
		text := repoFile(t, path)
		var candidates []string
		fenced := false
		for _, line := range strings.Split(text, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "```") {
				fenced = !fenced
				continue
			}
			if fenced {
				candidates = append(candidates, strings.TrimPrefix(trimmed, "$ "))
				continue
			}
			for _, m := range docCodeSpan.FindAllStringSubmatch(line, -1) {
				candidates = append(candidates, m[1])
			}
		}
		for _, candidate := range candidates {
			m := docCommandMention.FindStringSubmatch(candidate)
			if m == nil {
				continue
			}
			checked++
			if !registered[m[1]] {
				t.Errorf("%s: %q names no registered command %q", path, candidate, m[1])
				continue
			}
			if m[1] == "roster" && m[2] != "" && !roster[m[2]] {
				t.Errorf("%s: %q names no roster subcommand %q", path, candidate, m[2])
			}
		}
	}
	if checked == 0 {
		t.Fatal("no docflow command mention found: the scan is broken")
	}
}
