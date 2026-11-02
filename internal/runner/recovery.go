package runner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"bsv-docflow/internal/cycle"
)

// RunSummary is the closing record of the newest run stream of a checkout.
// It is read back when the terminal workspace reopens: the checkpoint names
// the interrupted work, the journal names how the last run ended.
type RunSummary struct {
	RunID      string
	Plan       string
	Reason     string
	ExitCode   int
	Paused     bool
	Unit       string
	UnitStatus string
	Stage      string
}

// Recovery is the saved state of an interrupted controlled run together with
// the last journal record. A checkpoint that no longer describes this
// checkout, or one whose unit already finished, is reported through Problem or
// Completed: Continue is never offered for it.
type Recovery struct {
	Checkpoint    *Checkpoint
	LastRun       *RunSummary
	Problem       string
	Completed     bool
	PendingCommit bool
}

// Resumable reports whether Continue may hand the checkpoint to ResumeRun.
func (r Recovery) Resumable() bool {
	return r.Checkpoint != nil && r.Problem == "" && !r.Completed
}

// LoadRecovery reads the checkpoint and the newest run journal of a checkout.
// A missing checkpoint is not an error: the workspace then only reports the
// outcome of the last run. A checkpoint is compared with the plan, the unit
// and the checkout it was taken in; a mismatch becomes Problem so the person
// is asked for an explicit new start instead of a silent continuation.
func LoadRecovery(root string) (Recovery, error) {
	recovery := Recovery{LastRun: readLastRun(root)}
	path, err := checkpointPath(root)
	if err != nil {
		// Not a checkout, or no state directory yet: nothing was saved.
		return recovery, nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return recovery, nil
	}
	checkpoint, err := ReadCheckpoint(root)
	if err != nil {
		recovery.Problem = fmt.Sprintf("the saved checkpoint cannot be read: %v", err)
		return recovery, nil
	}
	recovery.Checkpoint = &checkpoint
	plan, err := cycle.FindPlan(root, checkpoint.Plan)
	if err != nil {
		recovery.Problem = fmt.Sprintf("plan %s cannot be read: %v", checkpoint.Plan, err)
		return recovery, nil
	}
	empty := checkpoint.Unit == ""
	unit := plan.Unit(checkpoint.Unit)
	if !empty && unit == nil {
		recovery.Problem = fmt.Sprintf("unit %s is not in plan %s", checkpoint.Unit, checkpoint.Plan)
		return recovery, nil
	}
	if !empty && doneStatus(unit.Status) {
		pending, err := cycle.CommitPending(root, plan, unit)
		if err != nil {
			recovery.Problem = fmt.Sprintf("the commit state of %s cannot be read: %v", checkpoint.Unit, err)
			return recovery, nil
		}
		if !pending {
			// The paused unit was finished by a later run. There is no
			// interrupted work to continue, and showing its checkpoint as one
			// would invite a second run of a completed unit.
			recovery.Completed = true
			return recovery, nil
		}
		recovery.PendingCommit = true
	}
	if err := ValidateResumable(checkpoint); err != nil {
		recovery.Problem = err.Error()
		return recovery, nil
	}
	if err := ValidateCheckpoint(root, checkpoint); err != nil {
		recovery.Problem = err.Error()
		return recovery, nil
	}
	return recovery, nil
}

// StageName names the interrupted operation of a checkpoint for the UI.
func StageName(stage string) string { return stageName(stage) }

// readLastRun reads the newest event stream of the checkout line by line. A
// missing, unreadable or partially written stream is simply no summary: the
// journal is diagnostic, and a lost record must not hide the checkpoint.
func readLastRun(root string) *RunSummary {
	dir, err := runDir(root)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".events.jsonl") {
			continue
		}
		names = append(names, entry.Name())
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	name := names[len(names)-1]
	file, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return nil
	}
	defer file.Close()
	summary := &RunSummary{RunID: strings.TrimSuffix(name, ".events.jsonl")}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	seen := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil {
			continue
		}
		switch stringValue(record["event"]) {
		case "run_start":
			summary.Plan = stringValue(record["plan"])
			seen = true
		case "run_end":
			summary.Reason = stringValue(record["reason"])
			summary.ExitCode = intValueOf(record["exit_code"])
			summary.Paused = record["paused"] == true
			seen = true
		case "unit_end":
			if unit := stringValue(record["unit"]); unit != "" {
				summary.Unit = unit
				summary.UnitStatus = stringValue(record["status"])
			}
		case "state":
			if stringValue(record["state"]) == "Paused" {
				summary.Stage = stringValue(record["stage"])
			}
		}
	}
	if !seen {
		return nil
	}
	return summary
}

func intValueOf(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return 0
}
