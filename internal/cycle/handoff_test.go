package cycle

import (
	"fmt"
	"strings"
	"testing"

	"bsv-docflow/internal/format"
)

// handoffPlan builds a plan of units given as id -> Depends (`-` for none)
// in the order of ids; every unit carries one Handoff item `от <id>` padded
// with pad bytes of filler.
func handoffPlan(t *testing.T, ids []string, depends map[string]string, pad int) *format.Plan {
	t.Helper()
	var text strings.Builder
	text.WriteString("---\nid: P001\ntitle: Plan\ntype: feature\nstatus: active\ncreated: 2026-10-12\n---\n## Goal\nЦель.\n\n## Phase 1: First\n\n")
	for _, id := range ids {
		fmt.Fprintf(&text, "### [x] P001.1.%s Unit %s\nRead:\n- a.py\nDo:\n- b\nVerify: `true`\nDepends: %s\nCommit: P001.1.%s: unit\nHandoff:\n- от %s%s\n\n",
			id, id, depends[id], id, id, strings.Repeat("x", pad))
	}
	plan, err := format.ParsePlan(text.String(), "docs/plans/P001-plan.md")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func handoffOf(t *testing.T, plan *format.Plan, id string) string {
	t.Helper()
	unit := plan.Unit(id)
	if unit == nil {
		t.Fatalf("no unit %s", id)
	}
	return dependsHandoff(plan, unit)
}

// A → B → C: C sees the Handoff of A through B, marked with the unit it came
// through, after the direct one.
func TestDependsHandoffChain(t *testing.T) {
	plan := handoffPlan(t, []string{"1", "2", "3"}, map[string]string{"1": "-", "2": "P001.1.1", "3": "P001.1.2"}, 0)
	got := handoffOf(t, plan, "P001.1.3")
	want := "## Handoff from Depends\n\n" +
		"`Unverified:` items below stay open on their own units; do not repeat them in your `unverified`.\n\n" +
		"### P001.1.2\n- от 2\n\n" +
		"### P001.1.1 (via P001.1.2)\n- от 1\n\n"
	if got != want {
		t.Fatalf("handoff =\n%s\nwant\n%s", got, want)
	}
}

// A → B, A → C, B,C → D: D sees the Handoff of A once.
func TestDependsHandoffDiamondOnce(t *testing.T) {
	plan := handoffPlan(t, []string{"1", "2", "3", "4"}, map[string]string{"1": "-", "2": "P001.1.1", "3": "P001.1.1", "4": "P001.1.2, P001.1.3"}, 0)
	got := handoffOf(t, plan, "P001.1.4")
	if n := strings.Count(got, "- от 1\n"); n != 1 {
		t.Fatalf("Handoff of P001.1.1 appears %d times:\n%s", n, got)
	}
	order := []string{"### P001.1.2\n", "### P001.1.3\n", "### P001.1.1 (via P001.1.2)\n"}
	last := -1
	for _, part := range order {
		i := strings.Index(got, part)
		if i <= last {
			t.Fatalf("%q out of order in\n%s", part, got)
		}
		last = i
	}
}

// An acceptance unit lists every unit in Depends: all Handoff come in full as
// direct ones, each once, with no `via` blocks and no truncation.
func TestDependsHandoffAllDirect(t *testing.T) {
	plan := handoffPlan(t, []string{"1", "2", "3", "4"}, map[string]string{"1": "-", "2": "P001.1.1", "3": "P001.1.2", "4": "P001.1.1, P001.1.2, P001.1.3"}, farHandoffBudget)
	got := handoffOf(t, plan, "P001.1.4")
	for _, id := range []string{"1", "2", "3"} {
		if n := strings.Count(got, "### P001.1."+id+"\n"); n != 1 {
			t.Fatalf("P001.1.%s appears %d times:\n%s", id, n, got)
		}
	}
	if strings.Contains(got, "(via ") || strings.Contains(got, "Omitted") {
		t.Fatalf("direct Depends rendered as far or truncated:\n%s", got)
	}
}

// Far units come nearest first while they fit the budget; the first that
// does not fit ends the far part and is named with the rest. Direct ones are
// never cut.
func TestDependsHandoffBudget(t *testing.T) {
	ids := []string{"1", "2", "3", "4", "5", "6"}
	depends := map[string]string{"1": "-"}
	for i := 1; i < len(ids); i++ {
		depends[ids[i]] = "P001.1." + ids[i-1]
	}
	// Each far block is ~3000 bytes: two of them fit 8000, the third does not.
	plan := handoffPlan(t, ids, depends, 3000)
	got := handoffOf(t, plan, "P001.1.6")
	for _, part := range []string{"### P001.1.5\n", "### P001.1.4 (via P001.1.5)\n", "### P001.1.3 (via P001.1.4)\n"} {
		if !strings.Contains(got, part) {
			t.Fatalf("missing %q in\n%s", part, got)
		}
	}
	if strings.Contains(got, "- от 2") || strings.Contains(got, "- от 1") {
		t.Fatalf("over-budget Handoff rendered:\n%s", got)
	}
	if !strings.HasSuffix(got, "\nOmitted for size: P001.1.2, P001.1.1 — their Handoff is in the plan file.\n\n") {
		t.Fatalf("no truncation mark:\n%s", got)
	}
}

// A direct Depends Handoff over the budget still comes in full; only far
// ones are cut.
func TestDependsHandoffDirectNeverCut(t *testing.T) {
	plan := handoffPlan(t, []string{"1", "2", "3"}, map[string]string{"1": "-", "2": "P001.1.1", "3": "P001.1.2"}, farHandoffBudget)
	got := handoffOf(t, plan, "P001.1.3")
	if !strings.Contains(got, "### P001.1.2\n- от 2"+strings.Repeat("x", farHandoffBudget)+"\n") {
		t.Fatalf("direct Handoff cut:\n%.200s", got)
	}
	if strings.Contains(got, "(via ") || !strings.Contains(got, "Omitted for size: P001.1.1 —") {
		t.Fatalf("far Handoff over budget not omitted:\n%.200s", got)
	}
}

// Units without a Handoff are skipped but still lead to their own Depends.
func TestDependsHandoffThroughSilentUnit(t *testing.T) {
	plan := handoffPlan(t, []string{"1", "2", "3"}, map[string]string{"1": "-", "2": "P001.1.1", "3": "P001.1.2"}, 0)
	text := strings.Join(plan.Raw, "")
	text = strings.Replace(text, "Handoff:\n- от 2\n", "", 1)
	plan, err := format.ParsePlan(text, plan.Path)
	if err != nil {
		t.Fatal(err)
	}
	got := handoffOf(t, plan, "P001.1.3")
	if strings.Contains(got, "### P001.1.2") || !strings.Contains(got, "### P001.1.1 (via P001.1.2)\n- от 1\n") {
		t.Fatalf("handoff =\n%s", got)
	}
}
