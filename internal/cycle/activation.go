package cycle

import (
	"strings"

	"bsv-docflow/internal/format"
	"bsv-docflow/internal/gitx"
)

// CommitActivation commits the traces `docflow activate` leaves behind: the
// plan's `status: draft` -> `active` and, for a plan outside the Queue, its
// id appended there. Activation itself only prints a `commit:` hint for the
// operator; a run or a prompted agent has no operator standing by, and the
// dirty-tree gate of the first unit would refuse to start on those traces.
// The commit happens only when the working tree holds exactly those traces —
// any other change keeps the gate's refusal. It reports whether it committed.
func CommitActivation(root string, plan *format.Plan) (bool, error) {
	if plan.Meta["status"] != "active" {
		return false, nil
	}
	planPath := plan.Path
	docflowPath := format.DocflowFilePath
	dirtyPlan, dirtyDocflow := false, false
	lines, err := gitx.StatusLines(root)
	if err != nil {
		return false, err
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, " M ") {
			return false, nil
		}
		switch strings.TrimSpace(line[3:]) {
		case planPath:
			dirtyPlan = true
		case docflowPath:
			dirtyDocflow = true
		default:
			return false, nil
		}
	}
	if !dirtyPlan {
		return false, nil
	}
	added, removed, err := diffChangedLines(root, planPath)
	if err != nil {
		return false, err
	}
	if len(added) != 1 || added[0] != "status: active" || len(removed) != 1 || removed[0] != "status: draft" {
		return false, nil
	}
	if dirtyDocflow {
		added, removed, err = diffChangedLines(root, docflowPath)
		if err != nil {
			return false, err
		}
		if len(added) != 1 || added[0] != "- "+plan.Meta["id"] || len(removed) != 0 {
			return false, nil
		}
	}
	if _, err := gitx.Git(root, "add", "--", planPath, docflowPath); err != nil {
		return false, err
	}
	if _, err := gitx.Git(root, "commit", "-m", plan.Meta["id"]+": activate plan"); err != nil {
		return false, err
	}
	return true, nil
}

// diffChangedLines returns the added and removed lines of a file's unstaged
// diff, skipping the unified-diff headers.
func diffChangedLines(root, path string) (added, removed []string, err error) {
	diff, err := gitx.Git(root, "diff", "--", path)
	if err != nil {
		return nil, nil, err
	}
	for _, line := range strings.Split(string(diff), "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+"):
			added = append(added, line[1:])
		case strings.HasPrefix(line, "-"):
			removed = append(removed, line[1:])
		}
	}
	return added, removed, nil
}
