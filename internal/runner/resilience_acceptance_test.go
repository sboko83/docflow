package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// P056 acceptance: a plan with review units runs to the end without a human.
// Replan is on by default, a red gate on a clean tree stops the run once
// before any agent, a unit may repair a real foreign failure but a weakened
// check stays visible, and a review that does not converge stops for a human.
// Every scenario drives the built CLI over a temporary repository and a fake
// agent.

// resilienceFixture is the replan fixture without a `replan:` key, whose
// second unit is the phase review.
func resilienceFixture(t *testing.T) *runFixture {
	t.Helper()
	f := newReplanFixture(t)
	f.replace(planFile, "replan: claude | haiku | low\n", "review: claude | sonnet | high\n")
	f.replace(planFile, "Verify: `test -f d.txt`\nCommit: P001.1.2: second unit\n", "Verify: `test -f b.txt`\nRole: review\nCommit: P001.1.2: second unit\n")
	return f
}

// fixUnitReply is the replan agent's answer to the review block: a fix unit
// before the review, the review lifted back to `[ ]`.
func fixUnitReply() string {
	return editPlan("### [!] P001.1.2 Second unit", "### [ ] P001.1.3 Fix unit\nRead:\n- a.txt\nDo:\n- write e.txt\nVerify: `test -f e.txt`\nCommit: P001.1.3: fix unit\n\n### [ ] P001.1.2 Second unit") + doneReply()
}

func TestAcceptResilienceDefaultReplanFinishesReview(t *testing.T) {
	f := resilienceFixture(t)
	f.steps(writeReply("b.txt"), blockReply(), fixUnitReply(), writeReply("e.txt"), doneReply())
	r := f.cli("run", "--no-wait")
	f.check(r, 0, "P001.1.2 replan -> claude | opus | high", "P001: ready to close", ", 1 replan")
	subjects := f.git("log", "--format=%s")
	if !strings.HasPrefix(subjects, "P001.1.2: second unit\nP001.1.3: fix unit\nP001: replan P001.1.2\nP001.1.1: first unit\n") {
		t.Fatalf("commits:\n%s", subjects)
	}
	calls := f.calls()
	if len(calls) != 5 {
		t.Fatalf("calls %d != 5\n%s%s", len(calls), r.out, r.err)
	}
	if argv := strings.Join(calls[2].Argv, " "); !strings.Contains(argv, "--model opus") || !strings.Contains(calls[2].Stdin, "# Replan P001.1.2") {
		t.Fatalf("replan did not run on the executor route: %s", argv)
	}
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if !strings.Contains(flat(calls[2].Stdin), "a green run at replan time does not lift the block") {
		t.Fatal("the replan prompt lacks the reproduction rule")
	}
	if !strings.Contains(flat(calls[0].Stdin), "make the minimal repair and name it in `handoff`. Never weaken the check") {
		t.Fatal("the unit prompt lacks the bounded repair rule")
	}
	if status := f.git("status", "--porcelain"); status != "" {
		t.Fatalf("dirty tree after the run:\n%s", status)
	}

	off := resilienceFixture(t)
	off.steps(writeReply("b.txt"), blockReply())
	r = off.cli("run", "--no-wait", "--replans", "0")
	off.check(r, 3, "blocked (needs plan): "+blockedReason)
	if calls := off.calls(); len(calls) != 2 || strings.Contains(r.out, "replan ->") {
		t.Fatalf("--replans 0 still replanned: %d calls\n%s", len(calls), r.out)
	}
}

// The P052 incident: a gate that turned red between two runs (an expired
// fixture) stops the next run once at its start, before any agent, with the
// baseline log — not inside a unit and not twice.
func TestAcceptResilienceGateRedBetweenRuns(t *testing.T) {
	f := resilienceFixture(t)
	f.replace(planFile, "status: active", "status: active\ngate: test ! -f expired || { echo 'FAIL example/roster (fixture expired)'; exit 1; }")
	f.steps(writeReply("b.txt"))
	f.check(f.cli("run", "--no-wait", "--max-units", "1"), 0, "baseline: gate passed", "P001.1.1 done")
	f.write("expired", "2026-11-01\n")
	f.commit("the fixture date passed")
	f.steps(doneReply())
	r := f.cli("run", "--no-wait")
	f.check(r, 2, "FAIL example/roster (fixture expired)", "baseline: gate failed (exit 1)", "log: .git/docflow/run/baseline.1.out")
	if n := strings.Count(r.out+r.err, "baseline: gate failed"); n != 1 {
		t.Fatalf("baseline failure printed %d times:\n%s%s", n, r.out, r.err)
	}
	if calls := f.calls(); len(calls) != 0 {
		t.Fatalf("an agent ran on a red baseline: %d calls", len(calls))
	}
	if !strings.Contains(f.read(planFile), "### [ ] P001.1.2") || strings.Contains(r.out, "replan ->") {
		t.Fatalf("the red baseline touched the unit or replanned:\n%s", r.out)
	}
	if _, err := os.Stat(filepath.Join(f.root, ".git", "docflow", "run", "baseline.1.out")); err != nil {
		t.Fatal(err)
	}
}

// A unit repairs a real failure of a foreign test in its own commit and the
// repair shows in Test changes; a weakened test (a skip, a deleted file) is
// just as visible, so review and acceptance judge it.
func TestAcceptResilienceRepairShowsInTestChanges(t *testing.T) {
	for _, tc := range []struct{ name, step, item string }{
		{"repair", "printf 'func TestRoster() { want := \"2026-12-01\" }\\n' > roster_test.go\n", "roster_test.go: -1 lines"},
		{"skip", "printf 'func TestRoster() { t.Skip(\"later\") }\\n' > roster_test.go\n", "roster_test.go: +skip"},
		{"delete", "rm roster_test.go\n", "roster_test.go: deleted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := resilienceFixture(t)
			f.write("roster_test.go", "func TestRoster() { want := \"2026-11-01\" }\n")
			f.commit("foreign test")
			f.steps(tc.step + writeReply("b.txt"))
			r := f.cli("run", "--no-wait", "--max-units", "1")
			f.check(r, 0, "test change: "+tc.item)
			if body := f.git("log", "-1", "--format=%B"); !strings.Contains(body, tc.item) {
				t.Fatalf("the unit commit does not carry %q:\n%s", tc.item, body)
			}
		})
	}
}

// A review that blocks a third time stops the run for a human instead of
// another replan, although the per-call limits would allow one.
func TestAcceptResilienceReviewBudget(t *testing.T) {
	f := resilienceFixture(t)
	edit := "Role: review\nCommit: P001.1.2: second unit\nHandoff:\n"
	for i := 1; i <= 2; i++ {
		edit += fmt.Sprintf("- Blocked (plan): round %d\n", i)
	}
	f.replace(planFile, "Role: review\nCommit: P001.1.2: second unit\n", edit)
	f.steps(writeReply("b.txt"), blockReply())
	r := f.cli("run", "--no-wait")
	f.check(r, 3, "P001.1.2: blocked 3 times, review budget spent; decide by hand")
	if calls := f.calls(); len(calls) != 2 || strings.Contains(r.out, "replan ->") {
		t.Fatalf("the spent review replanned: %d calls\n%s", len(calls), r.out)
	}
	// The stop leaves only the block record in the plan, as any block does.
	if status := f.git("status", "--porcelain"); status != "M "+planFile {
		t.Fatalf("tree after the stop:\n%s", status)
	}
	if !strings.Contains(f.read(planFile), "### [!] P001.1.2") {
		t.Fatal("the spent review is not left blocked")
	}
}
