package stats

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBlockFixture(t *testing.T, root string) {
	t.Helper()
	plans := filepath.Join(root, "docs", "plans")
	if err := os.MkdirAll(plans, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := "---\nid: P900\ntitle: Fixture\ntype: feature\nstatus: active\ncreated: 2026-11-05\n---\n" +
		"## Goal\n\nx\n\n## Phase 1: One\n\n" +
		"### [!] P900.1.1 Review the phase\nRead:\n- a.go\nDo:\n- review\nVerify: `true`\nDepends: -\nRole: review\nCommit: P900.1.1: review\n\n" +
		"### [!] P900.1.2 Build\nRead:\n- a.go\nDo:\n- build\nVerify: `true`\nDepends: -\nRole: heavy\nCommit: P900.1.2: build\n"
	if err := os.WriteFile(filepath.Join(plans, "P900-fixture.md"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	runs := filepath.Join(root, ".git", "docflow", "run")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	var events strings.Builder
	for _, unit := range []string{"P900.1.1", "P900.1.1", "P900.1.1", "P900.1.2", "P900.1.2", "P900.1.2"} {
		events.WriteString(`{"event":"unit_end","unit":"` + unit + `","status":"blocked"}` + "\n")
	}
	events.WriteString(`{"event":"unit_end","unit":"P900.1.1","status":"done"}` + "\n")
	if err := os.WriteFile(filepath.Join(runs, "20261105T000000Z.events.jsonl"), []byte(events.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReviewBlockLinesSignalsThirdBlockOfReviewUnit(t *testing.T) {
	root, _ := statsProject(t)
	writeBlockFixture(t, root)
	got := reviewBlockLines(root, "")
	want := "review-blocks: P900.1.1 blocked 3 times: not converging, needs a human decision\n"
	if got != want {
		t.Fatalf("reviewBlockLines = %q; want %q (non-review unit must not signal)", got, want)
	}
	if other := reviewBlockLines(root, "P901"); other != "" {
		t.Fatalf("other plan filter = %q; want empty", other)
	}
}

func TestReviewBlockLinesQuietBelowThreshold(t *testing.T) {
	root, _ := statsProject(t)
	writeBlockFixture(t, root)
	path := filepath.Join(root, ".git", "docflow", "run", "20261105T000000Z.events.jsonl")
	data, _ := os.ReadFile(path)
	lines := strings.SplitN(string(data), "\n", 2)
	if err := os.WriteFile(path, []byte(lines[1]), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := reviewBlockLines(root, ""); got != "" {
		t.Fatalf("reviewBlockLines = %q; want empty with two blocks", got)
	}
}
