package roster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bsv-docflow/internal/format"
)

func TestAcceptanceReadProxyRangeAndMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "read.txt"), []byte("aaaa\nbbbb\ncccc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unit := &format.Unit{Fields: []format.Field{{Key: "Read", Items: []format.Item{{Text: "read.txt:L2-3"}, {Text: "missing.txt"}}}}}
	if got := readProxy(root, unit); got != 2 {
		t.Fatalf("ranged proxy with missing ref = %d, want 10 bytes / 4 = 2", got)
	}
}

func TestAcceptanceReadBudgetBoundary(t *testing.T) {
	policy, state, catalog := applyScenario(t)
	brief, err := BuildBrief(policy, state, catalog, []string{"cheap"})
	if err != nil {
		t.Fatal(err)
	}
	budget := brief.Roles[0].Budget.Target
	plan := strings.Replace(applyPlan(), "### [ ] P040.1.2 Second unit\nRead:\n- a.py", "### [ ] P040.1.2 Second unit\nRead:\n- boundary.txt", 1)
	root := applyProject(t, plan)
	for _, extra := range []int{0, 1} {
		if err := os.WriteFile(filepath.Join(root, "boundary.txt"), []byte(strings.Repeat("x", 4*(budget+extra))), 0o644); err != nil {
			t.Fatal(err)
		}
		result, _, err := Apply(root, "P040", policy, state, catalog, true)
		if err != nil || result.Code != 0 {
			t.Fatalf("apply = %+v, %v", result, err)
		}
		if got := strings.Contains(result.Stderr, "unit P040.1.2: Read: proxy"); got != (extra > 0) {
			t.Fatalf("boundary +%d warning = %t: %s", extra, got, result.Stderr)
		}
	}
}

func TestAcceptanceInlineStateAndEmptyMigration(t *testing.T) {
	state, err := ParseState("state.toml", "version = 2\nexpires = { zai = \"2099-01-01\" }\n")
	if err != nil {
		t.Fatal(err)
	}
	merged, err := Merge(stateTestPolicy(t), state)
	if err != nil {
		t.Fatal(err)
	}
	access, _ := merged.Access("zai")
	if access.Expires.Format("2006-01-02") != "2099-01-01" {
		t.Fatalf("inline expires = %v", access.Expires)
	}
	text, err := Migrate("spend = []\n" + policyRoles)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := ParsePolicy("empty.toml", text)
	if err != nil || len(policy.Accesses) != 0 {
		t.Fatalf("empty migration = %+v, %v", policy, err)
	}
}
