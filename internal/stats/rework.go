package stats

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"bsv-docflow/internal/gitx"
)

// ReworkWindow is how long after a unit's commit a commit made outside
// docflow still counts as rework of the unit.
const ReworkWindow = 7 * 24 * time.Hour

// docflowSubject matches the subjects docflow writes: unit commits
// (`P001.1.1: …`) and plan commits (`P001: replan …`, `P001: close`).
var docflowSubject = regexp.MustCompile(`^P\d+[.:]`)

// hunkOld matches the old-side range of a `-U0` hunk header.
var hunkOld = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? `)

// reworkCommits counts the commits within ReworkWindow after sha, outside
// docflow (no `Pxxx` subject, no merges), that change or delete a line the
// unit's commit wrote. A shared file every change touches (CHANGELOG, an
// index) does not count by itself: only the unit's own lines do. Plan
// bookkeeping (docs/plans, docs/archive, docs/docflow.md) never counts. ok is
// false when sha is not in this repository any more (a rewritten history).
func reworkCommits(root, sha string) (count int, ok bool) {
	head, err := gitx.Git(root, "show", "-s", "--format=%H%x00%ct", sha+"^{commit}")
	if err != nil {
		return 0, false
	}
	full, stamp, found := strings.Cut(strings.TrimSpace(string(head)), "\x00")
	at, err := strconv.ParseInt(stamp, 10, 64)
	if !found || err != nil {
		return 0, false
	}
	names, err := gitx.Git(root, "-c", "core.quotePath=false", "show", "--name-only", "--format=", "-z", full)
	if err != nil {
		return 0, false
	}
	var files []string
	for _, name := range strings.Split(string(names), "\x00") {
		if name == "" || strings.HasPrefix(name, "docs/plans/") || strings.HasPrefix(name, "docs/archive/") || name == "docs/docflow.md" {
			continue
		}
		files = append(files, name)
	}
	if len(files) == 0 {
		return 0, true
	}
	until := time.Unix(at, 0).Add(ReworkWindow).Unix()
	args := append([]string{"--literal-pathspecs", "log", "--no-merges", "--format=%H%x00%ct%x00%s", full + "..HEAD", "--"}, files...)
	log, err := gitx.Git(root, args...)
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
		parts := strings.SplitN(line, "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		when, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || when > until || docflowSubject.MatchString(parts[2]) {
			continue
		}
		if touchesLinesOf(root, parts[0], full, files) {
			count++
		}
	}
	return count, true
}

// touchesLinesOf reports whether commit changes or deletes a line of files
// that blame attributes to unit in the commit's parent.
func touchesLinesOf(root, commit, unit string, files []string) bool {
	args := append([]string{"--literal-pathspecs", "-c", "core.quotePath=false", "diff", "-U0", "--no-color", "--no-renames", "--no-ext-diff",
		"--src-prefix=a/", "--dst-prefix=b/", commit + "^", commit, "--"}, files...)
	diff, err := gitx.Git(root, args...)
	if err != nil {
		return false
	}
	path := ""
	for _, line := range strings.Split(string(diff), "\n") {
		switch {
		case strings.HasPrefix(line, "--- "):
			path = strings.TrimRight(strings.TrimPrefix(line, "--- "), "\r\t")
			if !strings.HasPrefix(path, "a/") {
				path = "" // a new file holds no line of the unit
				continue
			}
			path = strings.TrimPrefix(path, "a/")
		case path != "" && strings.HasPrefix(line, "@@ "):
			m := hunkOld.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			start, size := m[1], "1"
			if m[2] != "" {
				size = m[2]
			}
			if size == "0" {
				continue // a pure insertion changes no old line
			}
			blame, err := gitx.Git(root, "--literal-pathspecs", "blame", "--porcelain", "-L", start+",+"+size, commit+"^", "--", path)
			if err != nil {
				continue
			}
			for _, row := range strings.Split(string(blame), "\n") {
				if strings.HasPrefix(row, unit+" ") {
					return true
				}
			}
		}
	}
	return false
}
