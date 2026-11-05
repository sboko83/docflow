package stats

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

// BlockSignal is the number of blocked ends of one review unit at which stats
// warns that the review is not converging and a human should decide.
const BlockSignal = 3

// blockCounts counts `unit_end` events with status blocked per unit across the
// run event streams, limited to planID when it is not empty.
func blockCounts(root, planID string) (map[string]int, error) {
	verified, err := cycle.StateDir(root)
	if err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(verified), "run", "*.events.jsonl"))
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			var event struct {
				Event  string `json:"event"`
				Unit   string `json:"unit"`
				Status string `json:"status"`
			}
			if json.Unmarshal(scanner.Bytes(), &event) != nil {
				continue
			}
			if event.Event != "unit_end" || event.Status != "blocked" || event.Unit == "" {
				continue
			}
			if planID != "" && !strings.HasPrefix(event.Unit, planID+".") {
				continue
			}
			counts[event.Unit]++
		}
		file.Close()
	}
	return counts, nil
}

// reviewBlockLines lists review units blocked BlockSignal times or more; a
// unit whose plan cannot be read is skipped, since its role is unknown.
func reviewBlockLines(root, planID string) string {
	counts, err := blockCounts(root, planID)
	if err != nil || len(counts) == 0 {
		return ""
	}
	var units []string
	for unit, n := range counts {
		if n >= BlockSignal {
			units = append(units, unit)
		}
	}
	sort.SliceStable(units, func(i, j int) bool { return compareNumericParts(units[i], units[j]) < 0 })
	var out strings.Builder
	for _, unit := range units {
		_, found, err := cycle.FindUnit(root, unit)
		if err != nil || found == nil {
			continue
		}
		if role := found.Field("Role"); role == nil || role.Value != "review" {
			continue
		}
		fmt.Fprintf(&out, "review-blocks: %s blocked %d times: not converging, needs a human decision\n", unit, counts[unit])
	}
	return out.String()
}
