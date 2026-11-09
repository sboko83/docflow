package runner

import (
	"strings"
	"testing"

	"bsv-docflow/internal/format"
)

// An `Unverified:` item open on a unit reached through Depends of Depends is
// inherited too: the prompt shows it, so the runner drops its copy.
func TestDropInheritedUnverifiedTransitive(t *testing.T) {
	text := loopHeader +
		"### [x] P001.1.1 First unit\nRead:\n- docs/docflow.md\nDo:\n- a\nVerify: `true`\nDepends: -\nCommit: P001.1.1: first unit\nHandoff:\n- Unverified: поток A\n\n" +
		"### [x] P001.1.2 Second unit\nRead:\n- docs/docflow.md\nDo:\n- b\nVerify: `true`\nDepends: P001.1.1\nCommit: P001.1.2: second unit\nHandoff:\n- готово\n\n" +
		"### [~] P001.1.3 Third unit\nRead:\n- docs/docflow.md\nDo:\n- c\nVerify: `true`\nDepends: P001.1.2\nCommit: P001.1.3: third unit\n"
	plan, err := format.ParsePlan(strings.ReplaceAll(text, "{model}", "x"), planFile)
	if err != nil {
		t.Fatal(err)
	}
	kept, dropped := dropInheritedUnverified(plan, plan.Unit("P001.1.3"), []string{"поток A", "поток C"})
	if dropped != 1 || len(kept) != 1 || kept[0] != "поток C" {
		t.Fatalf("kept %q, dropped %d", kept, dropped)
	}
}
