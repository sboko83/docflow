package cycle

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"bsv-docflow/internal/gitx"
)

// A unit is allowed to edit tests, so a commit that weakens checks is not
// rejected: it is marked. Test change items name a test file that lost lines,
// a test file that was deleted, and an added skip marker. They go into the
// commit body under `Test changes:` and into the commit output as
// `test change: <item>` lines, and from there into review, acceptance and the
// run summary.

// skipMarkers are the added source lines that turn a test off.
var skipMarkers = []string{
	"t.Skip(",
	"@skip",
	"pytest.mark.skip",
	"@Ignore",
	"@Disabled",
	"XCTSkip",
	"xit(",
	"xdescribe(",
	"it.skip(",
	"describe.skip(",
	"test.skip(",
	".only(",
}

// testDirs are the path directories that hold tests.
var testDirs = map[string]bool{
	"test":        true,
	"tests":       true,
	"__tests__":   true,
	"spec":        true,
	"Tests":       true,
	"androidTest": true,
}

// IsTestPath reports whether path, given with slashes, names a file of tests:
// by its name pattern (Go, Python, JavaScript, Java, Kotlin, Swift) or by a
// test directory in its path.
func IsTestPath(path string) bool {
	path = filepath.ToSlash(path)
	if path == "" {
		return false
	}
	name := path
	if index := strings.LastIndex(path, "/"); index >= 0 {
		for _, dir := range strings.Split(path[:index], "/") {
			if testDirs[dir] {
				return true
			}
		}
		name = path[index+1:]
	}
	switch {
	case strings.HasSuffix(name, "_test.go"),
		strings.HasSuffix(name, "_test.py"),
		strings.HasSuffix(name, "Test.java"),
		strings.HasSuffix(name, "Test.kt"),
		strings.HasSuffix(name, "Tests.swift"),
		strings.HasSuffix(name, "Test.swift"),
		strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".py"),
		strings.Contains(name, ".test."),
		strings.Contains(name, ".spec."):
		return true
	}
	return false
}

// stagedTestChanges returns the test change items for root's index against
// HEAD. A repository without HEAD has no baseline and returns no items.
func stagedTestChanges(root string) ([]string, error) {
	if head(root) == nil {
		return nil, nil
	}
	return testChangesFor(root, "--cached")
}

// spanTestChanges returns the test change items of the commit at HEAD made by
// an earlier, interrupted Commit call. The index is clean against HEAD then,
// so the items come from the commit's own diff. before is the HEAD stored in
// the committing record, nil when the repository had no commits.
func spanTestChanges(root string, before any) ([]string, error) {
	text, ok := before.(string)
	if !ok || text == "" {
		return nil, nil
	}
	if head(root) == nil {
		return nil, nil
	}
	return testChangesFor(root, text, "HEAD")
}

// testChangesBody appends the `Test changes:` block to a commit message.
func testChangesBody(message string, items []string) string {
	if len(items) == 0 {
		return message
	}
	body := strings.TrimRight(message, "\n") + "\n\nTest changes:\n"
	for _, item := range items {
		body += fmt.Sprintf("- %s\n", item)
	}
	return body
}

// commitTestChanges picks the items of the `Test changes:` block out of the
// body of a commit message, the inverse of testChangesBody. A body without
// the block carries no items.
func commitTestChanges(body string) []string {
	items := []string{}
	inBlock := false
	for _, line := range strings.Split(normalizeNewlines(body), "\n") {
		switch text := strings.TrimSpace(line); {
		case !inBlock:
			inBlock = text == "Test changes:"
		case strings.HasPrefix(text, "- ") && len(text) > 2:
			items = append(items, text[len("- "):])
		default:
			inBlock = false
		}
	}
	return items
}

// testChangesFor runs three forms of one git diff and builds the items in the
// diff's own path order: numstat gives the line counts, name-status gives the
// file status letters, and the `-U0` patch gives the added lines.
func testChangesFor(root string, rev ...string) ([]string, error) {
	diff := func(args ...string) (string, error) {
		all := append([]string{"diff", "-M"}, append(rev, args...)...)
		out, err := gitx.Git(root, all...)
		return strings.ToValidUTF8(string(out), "\uFFFD"), err
	}
	numstat, err := diff("--numstat", "-z")
	if err != nil {
		return nil, err
	}
	status, err := diff("--name-status", "-z")
	if err != nil {
		return nil, err
	}
	patch, err := diff("-U0", "--no-prefix", "--no-color", "--no-ext-diff")
	if err != nil {
		return nil, err
	}
	codes := parseNameStatusZ(status)
	added := addedLinesByPath(patch)
	items := []string{}
	for _, entry := range parseNumstatZ(numstat) {
		if !IsTestPath(entry.path) {
			continue
		}
		switch codes[entry.path] {
		case "D":
			items = append(items, entry.path+": deleted")
			continue
		case "A":
			// A new test file lost no lines.
		}
		if entry.deleted > 0 {
			items = append(items, fmt.Sprintf("%s: -%d lines", entry.path, entry.deleted))
		}
		if hasSkipMarker(added[entry.path]) {
			items = append(items, entry.path+": +skip")
		}
	}
	return items, nil
}

// hasSkipMarker reports whether an added line turns a test off.
func hasSkipMarker(lines []string) bool {
	for _, line := range lines {
		for _, marker := range skipMarkers {
			if strings.Contains(line, marker) {
				return true
			}
		}
	}
	return false
}

type changeEntry struct {
	path    string
	deleted int
}

// parseNumstatZ parses `git diff --numstat -z`: a record is
// "<added>\t<deleted>\t<path>\0", and a rename record is
// "<added>\t<deleted>\t\0<from>\0<to>\0" with the destination path kept.
func parseNumstatZ(text string) []changeEntry {
	tokens := strings.Split(text, "\x00")
	entries := []changeEntry{}
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if token == "" || !strings.Contains(token, "\t") {
			continue
		}
		renamed := strings.HasSuffix(token, "\t")
		fields := strings.Split(strings.TrimSuffix(token, "\t"), "\t")
		if len(fields) < 2 {
			continue
		}
		path := ""
		if renamed {
			if i+2 >= len(tokens) {
				break
			}
			path = tokens[i+2]
			i += 2
		} else {
			if len(fields) < 3 {
				continue
			}
			path = strings.Join(fields[2:], "\t")
		}
		deleted, err := strconv.Atoi(fields[1])
		if err != nil {
			deleted = 0
		}
		entries = append(entries, changeEntry{path: path, deleted: deleted})
	}
	return entries
}

// parseNameStatusZ parses `git diff --name-status -z`: a record is
// "<X>\0<path>\0", a rename record is "<R100>\0<from>\0<to>\0". It keeps the
// status letter of each destination path.
func parseNameStatusZ(text string) map[string]string {
	tokens := strings.Split(text, "\x00")
	codes := map[string]string{}
	for i := 0; i < len(tokens); i++ {
		code := tokens[i]
		if code == "" || len(code) > 4 {
			continue
		}
		renamed := strings.HasPrefix(code, "R") || strings.HasPrefix(code, "C")
		if renamed {
			if i+2 >= len(tokens) {
				break
			}
			codes[tokens[i+2]] = code[:1]
			i += 2
		} else {
			if i+1 >= len(tokens) {
				break
			}
			codes[tokens[i+1]] = code[:1]
			i++
		}
	}
	return codes
}

// addedLinesByPath parses a `-U0 --no-prefix` patch and returns the added
// lines of each file, keyed by the destination path.
func addedLinesByPath(patch string) map[string][]string {
	added := map[string][]string{}
	source, target := "", ""
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			source, target = "", ""
		case strings.HasPrefix(line, "--- "):
			source = cleanDiffPath(line[4:])
		case strings.HasPrefix(line, "+++ "):
			target = cleanDiffPath(line[4:])
			if target == "/dev/null" {
				target = source
			}
		case strings.HasPrefix(line, "rename to "):
			target = cleanDiffPath(line[len("rename to "):])
		case strings.HasPrefix(line, "rename from "):
			source = cleanDiffPath(line[len("rename from "):])
		case strings.HasPrefix(line, "+"):
			if target != "" {
				added[target] = append(added[target], line[1:])
			}
		}
	}
	return added
}

// cleanDiffPath unquotes a path printed by a diff header.
func cleanDiffPath(value string) string {
	value = strings.TrimSuffix(value, "\r")
	if len(value) > 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		value = value[1 : len(value)-1]
	}
	return filepath.ToSlash(value)
}
