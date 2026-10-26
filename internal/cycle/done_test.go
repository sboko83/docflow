package cycle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bsv-docflow/internal/clierrors"
)

func donePlan(status string) string {
	unit := cycleUnit("P001.1.1", status)
	return strings.Replace(cyclePlan("P001", "active", unit), "- решение — почему — альтернатива\n", "", 1)
}

func makeDoneProject(t *testing.T, plan string) string {
	t.Helper()
	return makeCycleProject(t, cycleDocflow("P001"), map[string]string{"P001": plan}, map[string]string{"a.py": "print(1)\n"})
}

func runDone(root, unitID string, handoff, decisions []string) (Result, error) {
	return Done(root, unitID, handoff, decisions, nil)
}

func TestDoneRecordsHandoffDecisionsAndCommittingState(t *testing.T) {
	plan := donePlan("~")
	root := makeDoneProject(t, plan)
	if result, err := runVerify(root, "P001.1.1", nil, false); err != nil || result.Code != 0 {
		t.Fatalf("Verify() = %#v, %v", result, err)
	}
	result, err := runDone(root, "P001.1.1", []string{"первое", "второе\nв две строки"}, []string{"решение — почему — нет"})
	if err != nil || result.Code != 0 || result.Stdout != "P001.1.1: done\n" {
		t.Fatalf("Done() = %#v, %v", result, err)
	}
	expected := strings.Replace(plan, "### [~] P001.1.1", "### [x] P001.1.1", 1)
	expected = strings.Replace(expected, "Commit: P001.1.1: unit\n", "Commit: P001.1.1: unit\nHandoff:\n- первое\n- второе в две строки\n", 1)
	expected = strings.Replace(expected, "## Decisions\n", "## Decisions\n- решение — почему — нет\n", 1)
	if got := readPlanText(t, root); got != expected {
		t.Fatalf("saved plan =\n%s\nwant =\n%s", got, expected)
	}
	digest, err := fingerprint(root, "docs/plans/P001-plan.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	saved, found, err := savedFingerprint(root, "P001.1.1")
	if err != nil || !found || saved != digest {
		t.Fatalf("plan edit changed fingerprint: %q, %v; want %q", saved, err, digest)
	}
	path, err := committingPath(root, "P001.1.1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Heads     map[string]any `json:"heads"`
		Committed []any          `json:"committed"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Heads[""] == nil || record.Committed == nil || len(record.Committed) != 0 {
		t.Fatalf("committing record = %s", data)
	}
}

func TestDoneAppendsDecisionsAfterExistingText(t *testing.T) {
	plan := strings.Replace(donePlan("~"), "## Decisions\n", "## Decisions\n- старое\n  продолжение\n", 1)
	root := makeDoneProject(t, plan)
	if _, err := runVerify(root, "P001.1.1", nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := runDone(root, "P001.1.1", []string{"h"}, []string{"новое"}); err != nil {
		t.Fatal(err)
	}
	if got := readPlanText(t, root); !strings.Contains(got, "## Decisions\n- старое\n  продолжение\n- новое\n\n## Phase 1") {
		t.Fatalf("existing decisions were not kept:\n%s", got)
	}
}

func TestDoneNotVerifiedAndChangedTree(t *testing.T) {
	t.Run("not verified", func(t *testing.T) {
		plan := donePlan("~")
		root := makeDoneProject(t, plan)
		result, err := runDone(root, "P001.1.1", []string{"h"}, nil)
		if err != nil || result.Code != clierrors.EXIT_ORDER || !strings.Contains(result.Stderr, "not verified") {
			t.Fatalf("Done() = %#v, %v", result, err)
		}
		if got := readPlanText(t, root); got != plan {
			t.Fatalf("not-verified Done changed plan:\n%s", got)
		}
	})
	t.Run("tree changed", func(t *testing.T) {
		plan := donePlan("~")
		root := makeDoneProject(t, plan)
		if _, err := runVerify(root, "P001.1.1", nil, false); err != nil {
			t.Fatal(err)
		}
		writeCycleFile(t, root, "a.py", "print(3)\n")
		result, err := runDone(root, "P001.1.1", []string{"h"}, nil)
		if err != nil || result.Code != clierrors.EXIT_ORDER || !strings.Contains(result.Stderr, "tree changed after verify") {
			t.Fatalf("Done() = %#v, %v", result, err)
		}
		if got := readPlanText(t, root); got != plan {
			t.Fatalf("stale Done changed plan:\n%s", got)
		}
	})
}

func TestDoneWrongStatusAndHandoffRequired(t *testing.T) {
	t.Run("wrong status", func(t *testing.T) {
		root := makeDoneProject(t, donePlan(" "))
		if _, err := runVerify(root, "P001.1.1", nil, false); err != nil {
			t.Fatal(err)
		}
		result, err := runDone(root, "P001.1.1", []string{"h"}, nil)
		if err != nil || result.Code != clierrors.EXIT_ORDER || !strings.Contains(result.Stderr, "expected [~]") {
			t.Fatalf("Done() = %#v, %v", result, err)
		}
	})
	t.Run("handoff required", func(t *testing.T) {
		plan := donePlan("~")
		root := makeDoneProject(t, plan)
		if _, err := runVerify(root, "P001.1.1", nil, false); err != nil {
			t.Fatal(err)
		}
		if _, err := runDone(root, "P001.1.1", nil, nil); err == nil || err.Error() != "--handoff must not be empty" {
			t.Fatalf("missing handoff error = %v", err)
		}
		if _, err := runDone(root, "P001.1.1", []string{" \t"}, nil); err == nil || err.Error() != "--handoff must not be empty" {
			t.Fatalf("blank handoff error = %v", err)
		}
		if got := readPlanText(t, root); got != plan {
			t.Fatalf("invalid handoff changed plan:\n%s", got)
		}
	})
}

func TestDoneAddsSkipReasonToHandoff(t *testing.T) {
	root := makeDoneProject(t, donePlan("~"))
	reason := "нет сети"
	if _, err := runVerify(root, "P001.1.1", &reason, false); err != nil {
		t.Fatal(err)
	}
	if _, err := runDone(root, "P001.1.1", []string{"h"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := readPlanText(t, root); !strings.Contains(got, "Handoff:\n- h\n- Verify skipped: нет сети\n") {
		t.Fatalf("skip reason missing from handoff:\n%s", got)
	}
}

func TestDoneKeepsExistingHandoff(t *testing.T) {
	plan := strings.Replace(donePlan("~"), "Commit: P001.1.1: unit\n", "Commit: P001.1.1: unit\nHandoff:\n- Blocked: устарел\n  план\n", 1)
	root := makeDoneProject(t, plan)
	if _, err := runVerify(root, "P001.1.1", nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := runDone(root, "P001.1.1", []string{"новое"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := readPlanText(t, root); !strings.Contains(got, "Handoff:\n- Blocked: устарел\n  план\n- новое\n\n") {
		t.Fatalf("existing handoff was not kept:\n%s", got)
	}
}

func TestBlockDoingTodoAndDefaultNeeds(t *testing.T) {
	t.Run("doing", func(t *testing.T) {
		root := makeDoneProject(t, donePlan("~"))
		result, err := Block(root, "P001.1.1", "Do устарел", "plan")
		if err != nil || result.Code != 0 || result.Stdout != "P001.1.1: blocked (plan): Do устарел\n" {
			t.Fatalf("Block() = %#v, %v", result, err)
		}
		expected := strings.Replace(donePlan("~"), "### [~] P001.1.1", "### [!] P001.1.1", 1)
		expected = strings.Replace(expected, "Commit: P001.1.1: unit\n", "Commit: P001.1.1: unit\nHandoff:\n- Blocked (plan): Do устарел\n", 1)
		if got := readPlanText(t, root); got != expected {
			t.Fatalf("blocked plan =\n%s\nwant =\n%s", got, expected)
		}
		next, err := Next(root)
		if err != nil || next.Code != clierrors.EXIT_BLOCKED {
			t.Fatalf("Next() after block = %#v, %v", next, err)
		}
	})
	t.Run("todo default needs", func(t *testing.T) {
		root := makeDoneProject(t, donePlan(" "))
		result, err := Block(root, "P001.1.1", "нужен вход", "human")
		if err != nil || result.Stdout != "P001.1.1: blocked (human): нужен вход\n" {
			t.Fatalf("Block() = %#v, %v", result, err)
		}
		if got := readPlanText(t, root); !strings.Contains(got, "- Blocked (human): нужен вход\n") {
			t.Fatalf("default needs missing:\n%s", got)
		}
	})
}

func TestBlockAppendsAndRejectsWrongStatus(t *testing.T) {
	root := makeDoneProject(t, donePlan("~"))
	if _, err := Block(root, "P001.1.1", "первый", "human"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "docs", "plans", "P001-plan.md")
	plan, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(plan), "[!] P001.1.1", "[~] P001.1.1", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Block(root, "P001.1.1", "второй", "plan")
	if err != nil || result.Code != 0 {
		t.Fatalf("second Block() = %#v, %v", result, err)
	}
	if got := readPlanText(t, root); !strings.Contains(got, "Handoff:\n- Blocked (human): первый\n- Blocked (plan): второй\n") {
		t.Fatalf("block handoff was not appended:\n%s", got)
	}
	result, err = Block(root, "P001.1.1", "третий", "human")
	if err != nil || result.Code != clierrors.EXIT_ORDER || !strings.Contains(result.Stderr, "expected [ ] or [~]") {
		t.Fatalf("Block() with blocked status = %#v, %v", result, err)
	}
}

func TestBlockReasonRequired(t *testing.T) {
	root := makeDoneProject(t, donePlan("~"))
	if _, err := Block(root, "P001.1.1", "", "human"); err == nil || err.Error() != "--reason must not be empty" {
		t.Fatalf("missing reason error = %v", err)
	}
	if _, err := Block(root, "P001.1.1", " \t", "human"); err == nil || err.Error() != "--reason must not be empty" {
		t.Fatalf("blank reason error = %v", err)
	}
}

func TestUnblockResumesHumanBlock(t *testing.T) {
	root := makeDoneProject(t, donePlan("~"))
	if _, err := Block(root, "P001.1.1", "нужен вход в аккаунт", "human"); err != nil {
		t.Fatal(err)
	}
	result, err := Unblock(root, "P001.1.1", "вошёл\nв аккаунт")
	if err != nil || result.Code != clierrors.EXIT_OK || result.Stdout != "P001.1.1: unblocked\n" {
		t.Fatalf("Unblock() = %#v, %v", result, err)
	}
	expected := strings.Replace(donePlan("~"), "Commit: P001.1.1: unit\n", "Commit: P001.1.1: unit\nHandoff:\n- Blocked (human): нужен вход в аккаунт\n- Unblocked: вошёл в аккаунт\n", 1)
	if got := readPlanText(t, root); got != expected {
		t.Fatalf("unblocked plan =\n%s\nwant =\n%s", got, expected)
	}
	next, err := Next(root)
	if err != nil || next.Code != clierrors.EXIT_OK || next.Stdout != "P001.1.1\n" {
		t.Fatalf("Next() after unblock = %#v, %v", next, err)
	}
}

func TestUnblockEmptyNote(t *testing.T) {
	root := makeDoneProject(t, donePlan("~"))
	if _, err := Block(root, "P001.1.1", "нужен симулятор", "human"); err != nil {
		t.Fatal(err)
	}
	if result, err := Unblock(root, "P001.1.1", " \t"); err != nil || result.Code != clierrors.EXIT_OK {
		t.Fatalf("Unblock() with a blank note = %#v, %v", result, err)
	}
	if got := readPlanText(t, root); !strings.Contains(got, "- Unblocked: the person reports the blocked step done\n") {
		t.Fatalf("default note missing:\n%s", got)
	}
}

func TestUnblockRefusesPlanBlock(t *testing.T) {
	root := makeDoneProject(t, donePlan("~"))
	if _, err := Block(root, "P001.1.1", "Do устарел", "plan"); err != nil {
		t.Fatal(err)
	}
	before := readPlanText(t, root)
	result, err := Unblock(root, "P001.1.1", "сделал")
	if err != nil || result.Code != clierrors.EXIT_ORDER ||
		result.Stderr != "P001.1.1 is blocked (needs plan): replan it with bsv-docflow-plan\n" {
		t.Fatalf("Unblock() on a plan block = %#v, %v", result, err)
	}
	if result.Stdout != "" {
		t.Fatalf("stdout = %q", result.Stdout)
	}
	if got := readPlanText(t, root); got != before {
		t.Fatalf("plan block changed the plan:\n%s\nwant =\n%s", got, before)
	}
}

func TestUnblockLegacyBlockedItem(t *testing.T) {
	root := makeDoneProject(t, donePlan("!"))
	path := filepath.Join(root, "docs", "plans", "P001-plan.md")
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text = []byte(strings.Replace(string(text), "Commit: P001.1.1: unit\n", "Commit: P001.1.1: unit\nHandoff:\n- Blocked: устарел\n", 1))
	if err := os.WriteFile(path, text, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Unblock(root, "P001.1.1", "план обновлён")
	if err != nil || result.Code != clierrors.EXIT_OK {
		t.Fatalf("Unblock() on a legacy block = %#v, %v", result, err)
	}
	if got := readPlanText(t, root); !strings.Contains(got, "### [~] P001.1.1") ||
		!strings.Contains(got, "Handoff:\n- Blocked: устарел\n- Unblocked: план обновлён\n") {
		t.Fatalf("legacy block unblocked =\n%s", got)
	}
}

func TestUnblockIgnoresBlockedLookalikes(t *testing.T) {
	handoff := func(items ...string) string {
		text := "Commit: P001.1.1: unit\nHandoff:\n"
		for _, item := range items {
			text += "- " + item + "\n"
		}
		return text
	}
	plan := func(items ...string) string {
		return strings.Replace(donePlan("!"), "Commit: P001.1.1: unit\n", handoff(items...), 1)
	}
	t.Run("lookalikes do not hide a plan block", func(t *testing.T) {
		root := makeDoneProject(t, plan(append([]string{"Blocked (plan): Do устарел"}, blockedLookalikes...)...))
		before := readPlanText(t, root)
		result, err := Unblock(root, "P001.1.1", "сделал")
		if err != nil || result.Code != clierrors.EXIT_ORDER ||
			result.Stderr != "P001.1.1 is blocked (needs plan): replan it with bsv-docflow-plan\n" {
			t.Fatalf("Unblock() past lookalikes = %#v, %v", result, err)
		}
		if result.Stdout != "" {
			t.Fatalf("stdout = %q", result.Stdout)
		}
		if got := readPlanText(t, root); got != before {
			t.Fatalf("Unblock() past lookalikes changed the plan:\n%s\nwant =\n%s", got, before)
		}
	})
	t.Run("lookalikes alone keep the block classless", func(t *testing.T) {
		root := makeDoneProject(t, plan(append(blockedLookalikes, "Unblocked: поправил")...))
		if _, unit, err := findUnit(root, "P001.1.1"); err != nil {
			t.Fatal(err)
		} else if needs, reason := BlockedNeeds(unit); needs != "" || reason != "" {
			t.Fatalf("BlockedNeeds = %q, %q, want none", needs, reason)
		}
		result, err := Unblock(root, "P001.1.1", "готово")
		if err != nil || result.Code != clierrors.EXIT_OK {
			t.Fatalf("Unblock() on a unit blocked by hand = %#v, %v", result, err)
		}
		if got := readPlanText(t, root); !strings.Contains(got, "### [~] P001.1.1 Unit P001.1.1\n") ||
			!strings.Contains(got, "- Unblocked: готово\n") {
			t.Fatalf("unblocked by hand =\n%s", got)
		}
	})
}

func TestUnblockRejectsWrongStatus(t *testing.T) {
	for _, status := range []string{" ", "~", "x"} {
		root := makeDoneProject(t, donePlan(status))
		result, err := Unblock(root, "P001.1.1", "готово")
		if err != nil || result.Code != clierrors.EXIT_ORDER ||
			result.Stderr != fmt.Sprintf("P001.1.1 is [%s], expected [!]\n", status) {
			t.Fatalf("Unblock() on [%s] = %#v, %v", status, result, err)
		}
		if got := readPlanText(t, root); got != donePlan(status) {
			t.Fatalf("Unblock() on [%s] changed the plan:\n%s", status, got)
		}
	}
}

func readPlanText(t *testing.T, root string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, "docs", "plans", "P001-plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
