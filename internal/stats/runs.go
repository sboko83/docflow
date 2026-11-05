package stats

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/cycle"
)

// RunRow summarises one unit across all run event streams.
type RunRow struct {
	Unit        string
	Version     string  // docflow version of the latest run_start seen before the unit's events
	Attempts    int     // attempt events, replan attempts excluded
	VerifyFails int     // verify events with a non-zero exit code
	Replans     int     // replan events
	Blocked     int     // unit_end with status blocked
	Done        bool    // a unit_end with status done was seen
	TestChanges int     // sum of unit_end test_changes
	Seconds     float64 // sum of unit_end seconds
	Cost        float64 // sum of unit_end cost
}

type runEvent struct {
	Event       string   `json:"event"`
	Version     string   `json:"version"`
	Unit        string   `json:"unit"`
	Status      string   `json:"status"`
	ExitCode    int      `json:"exit_code"`
	Replan      bool     `json:"replan"`
	Close       bool     `json:"close"`
	TestChanges int      `json:"test_changes"`
	Seconds     float64  `json:"seconds"`
	Cost        *float64 `json:"cost"`
}

// RunRows folds the run event streams into one row per unit, limited to
// planID when it is not empty. Unknown events and unreadable lines are skipped.
func RunRows(root, planID string) ([]RunRow, error) {
	state, err := cycle.StateDir(root)
	if err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(state), "run", "*.events.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	rows := make(map[string]*RunRow)
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		version := ""
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			var event runEvent
			if json.Unmarshal(scanner.Bytes(), &event) != nil {
				continue
			}
			if event.Event == "run_start" {
				version = event.Version
			}
			if event.Unit == "" || event.Close {
				continue
			}
			if planID != "" && !strings.HasPrefix(event.Unit, planID+".") {
				continue
			}
			row := rows[event.Unit]
			if row == nil {
				row = &RunRow{Unit: event.Unit}
				rows[event.Unit] = row
			}
			if version != "" {
				row.Version = version
			}
			switch event.Event {
			case "attempt":
				if !event.Replan {
					row.Attempts++
				}
			case "verify":
				if event.ExitCode != 0 {
					row.VerifyFails++
				}
			case "replan":
				row.Replans++
			case "unit_end":
				if event.Status == "blocked" {
					row.Blocked++
				}
				if event.Status == "done" {
					row.Done = true
				}
				row.TestChanges += event.TestChanges
				row.Seconds += event.Seconds
				if event.Cost != nil {
					row.Cost += *event.Cost
				}
			}
		}
		file.Close()
	}
	out := make([]RunRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, *row)
	}
	sort.SliceStable(out, func(i, j int) bool { return compareNumericParts(out[i].Unit, out[j].Unit) < 0 })
	return out, nil
}

// RunsCommand prints the per-unit run summary.
func RunsCommand(root, planID string) (Result, error) {
	rows, err := RunRows(root, planID)
	if err != nil {
		return Result{}, err
	}
	if len(rows) == 0 {
		return Result{Code: clierrors.EXIT_NOTHING, Stderr: "no runs: no run events recorded\n"}, nil
	}
	var out strings.Builder
	out.WriteString("unit version attempts verify_fails replans blocked test_changes seconds cost done\n")
	for _, row := range rows {
		cost := "-"
		if row.Cost > 0 {
			cost = fmt.Sprintf("%.4f", row.Cost)
		}
		done := "no"
		if row.Done {
			done = "yes"
		}
		version := row.Version
		if version == "" {
			version = "-"
		}
		fmt.Fprintf(&out, "%s %s %d %d %d %d %d %.1f %s %s\n", row.Unit, version, row.Attempts,
			row.VerifyFails, row.Replans, row.Blocked, row.TestChanges, row.Seconds, cost, done)
	}
	return Result{Stdout: out.String()}, nil
}
