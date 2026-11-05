package stats

import (
	"fmt"
	"strings"

	"bsv-docflow/internal/cycle"
)

// BlockSignal is the number of blocked ends of one review unit at which stats
// warns that the review is not converging and a human should decide.
const BlockSignal = 3

// reviewBlockLines lists review units blocked BlockSignal times or more in the
// run event streams (RunRows counts `unit_end` with status blocked); a unit
// whose plan cannot be read is skipped, since its role is unknown.
func reviewBlockLines(root, planID string) string {
	rows, err := RunRows(root, planID)
	if err != nil {
		return ""
	}
	var out strings.Builder
	for _, row := range rows {
		if row.Blocked < BlockSignal {
			continue
		}
		_, found, err := cycle.FindUnit(root, row.Unit)
		if err != nil || found == nil {
			continue
		}
		if role := found.Field("Role"); role == nil || role.Value != "review" {
			continue
		}
		fmt.Fprintf(&out, "review-blocks: %s blocked %d times: not converging, needs a human decision\n", row.Unit, row.Blocked)
	}
	return out.String()
}
