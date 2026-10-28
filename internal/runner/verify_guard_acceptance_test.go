package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bsv-docflow/internal/testshim"
)

// The end-to-end acceptance of the verify guard (plan P050): a run through
// the built CLI with fake agents exercises the whole chain — the unit commit
// that marks weakened tests, the review and acceptance prompts that carry
// the marks, and the replan guard that stops an agent silently weakening a
// check of its own work.

const guardHeader = "---\nid: P001\ntitle: Guard\ntype: feature\nstatus: active\ncreated: 2026-10-30\nexecutor: claude | claude-opus-5-5 | high\nreplan: claude | claude-haiku | low\nreview: claude | claude-sonnet-5-5 | high\nacceptance: claude | claude-sonnet-5-5 | high\n---\n## Goal\nGuard the checks.\n\n## Context\n\n## Constraints\n\n## Traps\n\n## Decisions\n\n## Phase 1: Work\n\n"

// The Verify values are unique strings: replan steps edit them by literal
// replacement and must hit exactly the intended unit.
const guardUnits = "### [ ] P001.1.1 Work unit\nRead:\n- docs/docflow.md\nDo:\n- write a.txt\nVerify: `test -f a.txt`\nCommit: P001.1.1: work unit\n\n" +
	"### [ ] P001.1.2 Review unit\nRead:\n- docs/docflow.md\nDo:\n- review the phase\nVerify: `echo review done`\nRole: review\nCommit: P001.1.2: review unit\n\n" +
	"### [ ] P001.1.3 Accept unit\nRead:\n- docs/docflow.md\nDo:\n- accept the plan\nVerify: `echo accept done`\nDepends: P001.1.1\nRole: acceptance\nCommit: P001.1.3: accept unit\n"

// guardFixture is a plan of one ordinary unit followed by its phase review
// and the plan acceptance, with two baseline test files the ordinary unit
// weakens: a_test.go loses a line, b_test.go gains a skip marker.
func guardFixture(t *testing.T) *runFixture {
	t.Helper()
	f := newRunFixture(t)
	// The plan assigns executors: the run must not carry --config and the
	// chains name a built-in agent, a claude stub in PATH that execs the
	// fake scenario agent.
	fake, _ := filepath.Abs("../agentio/testdata/fake_agent.sh")
	bin := filepath.Join(f.tmp, "bin")
	testshim.Script(t, bin, "claude", "#!/bin/sh\ncase \"$*\" in *\"/usage\"*) exit 0 ;; esac\nexec "+shellQuote(testshim.Bash(t))+" "+shellQuote(filepath.ToSlash(fake))+" \"$@\"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	f.write(planFile, guardHeader+guardUnits)
	f.write("a_test.go", "package a\nfunc TestA() {}\nfunc TestB() {}\n")
	f.write("b_test.go", "package b\nfunc TestB() {}\n")
	f.commit("guard")
	return f
}

// runBare runs the CLI without --config, the way a plan with its own
// executor chains must be run.
func (f *runFixture) runBare(steps []string, args ...string) runResult {
	f.t.Helper()
	f.steps(steps...)
	return f.cli(append([]string{"run", "--no-wait"}, args...)...)
}

// weakenStep is the work unit's agent: it writes its deliverable and
// weakens the tests of the repository in one commit.
func weakenStep() string {
	return "printf 'a\\n' > a.txt\n" +
		"printf 'package a\\nfunc TestA() {}\\n' > a_test.go\n" +
		"printf 'package b\\nfunc TestB() {}\\nfunc TestSkipped() { t.Skip(\"flaky\") }\\n' > b_test.go\n" +
		doneReply()
}

// Scenario 1: the run completes, the unit's commit body carries the
// `Test changes:` block with both items, the run output prints the
// `test change:` lines and the summary counter, unit_end reports 2.
func TestVerifyGuardRunMarksTestChanges(t *testing.T) {
	f := guardFixture(t)
	r := f.runBare([]string{weakenStep(), doneReply(), doneReply()})
	f.check(r, 0, "run: 3 units",
		"test change: a_test.go: -1 lines",
		"test change: b_test.go: +skip",
		", 2 test changes")
	body := f.git("log", "-1", "--format=%B", "--grep=^P001.1.1: work unit$")
	for _, want := range []string{"\nTest changes:\n- a_test.go: -1 lines", "- b_test.go: +skip"} {
		if !strings.Contains(body, want) {
			t.Errorf("commit body has no %q: %q", want, body)
		}
	}
	var items []any
	for _, record := range fixtureEvents(t, f) {
		switch record["event"] {
		case "commit":
			if record["unit"] == "P001.1.1" {
				items, _ = record["test_changes"].([]any)
			}
		case "unit_end":
			want := float64(0)
			if record["unit"] == "P001.1.1" {
				want = 2
			}
			if record["test_changes"] != want {
				t.Errorf("unit_end %v test_changes = %v, want %v", record["unit"], record["test_changes"], want)
			}
		}
	}
	if len(items) != 2 || items[0] != "a_test.go: -1 lines" || items[1] != "b_test.go: +skip" {
		t.Fatalf("commit event test_changes = %v:\n%s", items, r.out)
	}
}

// Scenario 2: after the weakened commit, the phase review prompt and the
// plan acceptance prompt carry the `## Test changes` section with the same
// items; the review section follows the phase diff, the acceptance prompt
// has no phase diff. A prompt marks its unit [~], so each subtest prompts
// one unit of its own fixture.
func TestVerifyGuardPromptsCarryTestChanges(t *testing.T) {
	section := "## Test changes\n\n- P001.1.1: work unit: a_test.go: -1 lines\n- P001.1.1: work unit: b_test.go: +skip\n"
	t.Run("review", func(t *testing.T) {
		f := guardFixture(t)
		f.check(f.runBare([]string{weakenStep()}, "--max-units", "1"), 0, "P001.1.1 done:")
		review := f.cli("prompt", "P001.1.2")
		f.check(review, 0)
		if !strings.Contains(review.out, section) {
			t.Errorf("review prompt has no test changes section:\n%s", review.out)
		}
		phase, at, cycle := strings.Index(review.out, "## Phase diff"), strings.Index(review.out, "## Test changes"), strings.Index(review.out, "## Cycle")
		if !(phase >= 0 && at >= 0 && cycle >= 0 && phase < at && at < cycle) {
			t.Errorf("review section placement: phase %d, test changes %d, cycle %d", phase, at, cycle)
		}
	})
	t.Run("acceptance", func(t *testing.T) {
		f := guardFixture(t)
		f.check(f.runBare([]string{weakenStep()}, "--max-units", "1"), 0, "P001.1.1 done:")
		acceptance := f.cli("prompt", "P001.1.3")
		f.check(acceptance, 0)
		if !strings.Contains(acceptance.out, section) {
			t.Errorf("acceptance prompt has no test changes section:\n%s", acceptance.out)
		}
		if strings.Contains(acceptance.out, "## Phase diff") {
			t.Errorf("acceptance prompt has a phase diff:\n%s", acceptance.out)
		}
	})
}

// replanSteps builds the two-step start of a guarded replan scenario: the
// work unit blocks with blocker plan, then the replan agent edits the plan.
func replanSteps(edit string) []string {
	return []string{blockReply(), "cp " + planFile + " .git/replan-before\n" + edit + doneReply()}
}

func restoredPlan(t *testing.T, f *runFixture) {
	t.Helper()
	if f.read(planFile) != f.read(".git/replan-before") {
		t.Fatalf("plan was not restored byte-for-byte:\n%s", f.read(planFile))
	}
}

// Scenario 3: the replan agent rewrites the Verify of the acceptance unit:
// the run stops with code 3 naming the unit and the field, the plan is
// restored byte-for-byte and nothing is recorded.
func TestVerifyGuardReplanRejectsAnchorVerifyChange(t *testing.T) {
	f := guardFixture(t)
	edit := editPlan("Verify: `echo accept done`", "Verify: `true`") + editPlan("### [!] P001.1.1", "### [~] P001.1.1")
	r := f.runBare(replanSteps(edit))
	f.check(r, 3, "unit P001.1.3 (acceptance): Verify changed")
	restoredPlan(t, f)
	if strings.Contains(f.git("log", "--format=%s"), "P001: replan") {
		t.Fatalf("a rejected replan was committed:\n%s", f.git("log", "--format=%s"))
	}
	if strings.Contains(f.read(planFile), "Replan changed check:") {
		t.Fatalf("a rejected replan recorded a check change:\n%s", f.read(planFile))
	}
	for _, record := range fixtureEvents(t, f) {
		if record["event"] == "replan" {
			if changes, ok := record["check_changes"].([]any); !ok || len(changes) != 0 {
				t.Fatalf("replan event check_changes = %v, want empty", record["check_changes"])
			}
		}
	}
}

// Scenario 4: the replan agent weakens the Verify of an ordinary unit: the
// run continues, docflow itself records the change in ## Decisions, the
// replan commit carries it, the summary counts it, the replan event reports
// it, and the review and acceptance prompts of the same run show both the
// recorded line and the test changes section.
func TestVerifyGuardReplanRecordsCheckChange(t *testing.T) {
	f := guardFixture(t)
	item := "P001.1.1 Verify: `test -f a.txt` -> `true`"
	edit := editPlan("Verify: `test -f a.txt`", "Verify: `true`") + editPlan("### [!] P001.1.1", "### [~] P001.1.1")
	r := f.runBare(append(replanSteps(edit), writeReply("a.txt"), doneReply(), doneReply()))
	f.check(r, 0, "P001: ready to close",
		"P001.1.1: replan changed check: "+item,
		", 1 checks changed by replan")
	if !strings.Contains(f.read(planFile), "- Replan changed check: "+item) {
		t.Fatalf("plan Decisions carry no record:\n%s", f.read(planFile))
	}
	if got := f.git("log", "-S- Replan changed check: "+item, "--format=%s"); !strings.Contains(got, "P001: replan P001.1.1") {
		t.Fatalf("replan commit does not carry the Decisions line: %s", got)
	}
	calls := f.calls()
	if len(calls) != 5 {
		t.Fatalf("calls = %d, want 5 (unit, replan, retry, review, acceptance):\n%s", len(calls), r.out)
	}
	for _, call := range calls[3:5] {
		prompt := call.Stdin
		if !strings.Contains(prompt, "Replan changed check: "+item) {
			t.Errorf("prompt carries no recorded check change:\n%s", prompt)
		}
		if !strings.Contains(prompt, "## Test changes\n\nnone\n") {
			t.Errorf("prompt carries no test changes section:\n%s", prompt)
		}
	}
	var changes []any
	for _, record := range fixtureEvents(t, f) {
		if record["event"] == "replan" {
			changes, _ = record["check_changes"].([]any)
		}
	}
	if len(changes) != 1 || changes[0] != item {
		t.Fatalf("replan event check_changes = %v, want [%s]", changes, item)
	}
}

// Scenario 5: the replan agent marks an open unit [x]: the run stops with
// code 3 naming the unit, the plan is restored and nothing is recorded.
// The ordinary-unit case also weakens the Verify of the same unit: the
// marked-[x] problem is the only one and check_changes stays empty.
func TestVerifyGuardReplanRejectsMarkedDone(t *testing.T) {
	t.Run("ordinary unit weakened and marked", func(t *testing.T) {
		f := newReplanFixture(t)
		edit := editPlan("### [ ] P001.1.2 Second unit", "### [x] P001.1.2 Second unit") +
			editPlan("Verify: `test -f d.txt`", "Verify: `true`") +
			editPlan("### [!] P001.1.1", "### [~] P001.1.1")
		r := f.runBare(replanSteps(edit))
		f.check(r, 3, "unit P001.1.2 marked [x] by replan")
		if strings.Contains(r.out+r.err, "replan changed check") {
			t.Fatalf("a rejected replan recorded a check change:\n%s%s", r.out, r.err)
		}
		restoredPlan(t, f)
		for _, record := range fixtureEvents(t, f) {
			if record["event"] == "replan" {
				if changes, ok := record["check_changes"].([]any); !ok || len(changes) != 0 {
					t.Fatalf("replan event check_changes = %v, want empty", record["check_changes"])
				}
			}
		}
	})
	t.Run("acceptance unit", func(t *testing.T) {
		f := guardFixture(t)
		edit := editPlan("### [ ] P001.1.3 Accept unit", "### [x] P001.1.3 Accept unit") +
			editPlan("### [!] P001.1.1", "### [~] P001.1.1")
		r := f.runBare(replanSteps(edit))
		f.check(r, 3, "unit P001.1.3 marked [x] by replan")
		if strings.Contains(r.out+r.err, "(acceptance)") {
			t.Fatalf("the anchor branch ran on a unit marked [x]:\n%s%s", r.out, r.err)
		}
		restoredPlan(t, f)
	})
}

// A replan that moves an ordinary open unit into [-] is allowed and
// recorded end to end: the Decisions line, the run line, the summary
// counter and the replan event; the skipped unit never runs.
func TestVerifyGuardReplanSkipEndToEnd(t *testing.T) {
	f := newReplanFixture(t)
	edit := editPlan("### [ ] P001.1.2 Second unit", "### [-] P001.1.2 Second unit") +
		editPlan("### [!] P001.1.1", "### [~] P001.1.1")
	r := f.runBare(append(replanSteps(edit), writeReply("b.txt")))
	f.check(r, 0, "P001: ready to close",
		"P001.1.1: replan changed check: P001.1.2 skipped",
		", 1 checks changed by replan")
	if !strings.Contains(f.read(planFile), "- Replan changed check: P001.1.2 skipped") {
		t.Fatalf("plan Decisions carry no record:\n%s", f.read(planFile))
	}
	if got := f.git("log", "-S- Replan changed check: P001.1.2 skipped", "--format=%s"); !strings.Contains(got, "P001: replan P001.1.1") {
		t.Fatalf("replan commit does not carry the Decisions line: %s", got)
	}
	if calls := f.calls(); len(calls) != 3 {
		t.Fatalf("calls = %d, want 3 (unit, replan, retry); the skipped unit must not run:\n%s", len(calls), r.out)
	}
	for _, record := range fixtureEvents(t, f) {
		if record["event"] == "replan" {
			changes, _ := record["check_changes"].([]any)
			if len(changes) != 1 || changes[0] != "P001.1.2 skipped" {
				t.Fatalf("replan event check_changes = %v", changes)
			}
		}
	}
}

// perlEdit replaces text in the plan file byte-for-byte: unlike the awk
// of Git Bash, perl keeps the CRLF line endings of a Windows plan.
func perlEdit(old, new string) string {
	return "PERL_OLD=" + shellQuote(old) + " PERL_NEW=" + shellQuote(new) +
		" perl -pi -e 's/\\Q$ENV{PERL_OLD}\\E/$ENV{PERL_NEW}/' " + planFile + "\n"
}

// A plan written with CRLF keeps its line endings through a replan: the
// item docflow appends to ## Decisions ends with the plan's own CRLF.
func TestVerifyGuardReplanOnCRLFPlan(t *testing.T) {
	f := newReplanFixture(t)
	f.write(planFile, strings.ReplaceAll(f.read(planFile), "\n", "\r\n"))
	f.commit("crlf plan")
	item := "P001.1.1 Verify: `test -f b.txt` -> `true`"
	edit := perlEdit("Verify: `test -f b.txt`", "Verify: `true`") + perlEdit("### [!] P001.1.1", "### [~] P001.1.1")
	r := f.runBare(append(replanSteps(edit), writeReply("b.txt"), writeReply("d.txt")))
	f.check(r, 0, "P001: ready to close",
		"P001.1.1: replan changed check: "+item,
		", 1 checks changed by replan")
	plan := f.read(planFile)
	if !strings.Contains(plan, "- Replan changed check: "+item) {
		t.Fatalf("plan Decisions carry no record:\n%s", plan)
	}
	if !strings.Contains(plan, "- Replan changed check: "+item+"\r\n") {
		t.Fatalf("the appended item does not keep the plan's CRLF:\n%q", plan)
	}
}

// A run with a nested repository prints the nested test change with the
// repository prefix, and the review and acceptance prompts carry the
// per-repository blocks of the section.
func TestVerifyGuardNestedRepoTestChanges(t *testing.T) {
	f := guardFixture(t)
	f.write("docs/docflow.md", strings.Replace(loopDoc, "read_budget: 40000\n", "read_budget: 40000\nrepos: deps/*\n", 1))
	f.write(".gitignore", "deps/module\n")
	nested := filepath.Join(f.root, "deps", "module")
	f.write(filepath.Join("deps", "module", "inner_test.go"), "package inner\nfunc TestA() {}\nfunc TestB() {}\n")
	gitAt(t, nested, "init", "-q")
	gitAt(t, nested, "config", "user.name", "t")
	gitAt(t, nested, "config", "user.email", "t@example.invalid")
	gitAt(t, nested, "add", "-A")
	gitAt(t, nested, "commit", "-qm", "nested base")
	f.commit("repos")
	step := "printf 'a\\n' > a.txt\nprintf 'package inner\\nfunc TestA() {}\\n' > deps/module/inner_test.go\n" + doneReply()
	r := f.runBare([]string{step}, "--max-units", "1")
	f.check(r, 0, "P001.1.1 done:",
		"test change: deps/module/inner_test.go: -1 lines",
		", 1 test changes")
	if body := gitAt(t, nested, "log", "-1", "--format=%B"); !strings.Contains(body, "\nTest changes:\n- inner_test.go: -1 lines") {
		t.Fatalf("nested commit body has no test changes block: %q", body)
	}
	if body := f.git("log", "-1", "--format=%B"); !strings.Contains(body, "Repo: deps/module ") {
		t.Fatalf("root commit body names no nested repository: %q", body)
	}
	// A prompt marks its unit [~]; the plan is reset between the two.
	plan := f.read(planFile)
	for _, unit := range []string{"P001.1.2", "P001.1.3"} {
		prompt := f.cli("prompt", unit)
		f.check(prompt, 0)
		for _, want := range []string{
			"### .\n\nnone",
			"### deps/module\n\n- P001.1.1: work unit: inner_test.go: -1 lines\n",
		} {
			if !strings.Contains(prompt.out, want) {
				t.Errorf("%s prompt has no %q:\n%s", unit, want, prompt.out)
			}
		}
		f.write(planFile, plan)
	}
	for _, record := range fixtureEvents(t, f) {
		if record["event"] == "unit_end" && record["unit"] == "P001.1.1" && record["test_changes"] != float64(1) {
			t.Fatalf("unit_end test_changes = %v, want 1", record["test_changes"])
		}
	}
}

// A crash between the unit's git commit and the commit command's output
// leaves the pending-commit journal; the resumed run prints the recovered
// test change items, counts them in the summary and reports them in the
// commit event and unit_end.
func TestVerifyGuardRecoveredCommitPrintsTestChanges(t *testing.T) {
	f := newRunFixture(t)
	f.write("a_test.go", "package a\nfunc TestA() {}\nfunc TestB() {}\n")
	f.commit("tests")
	f.check(f.cli("prompt", "P001.1.1"), 0)
	f.write("a.txt", "a\n")
	f.write("a_test.go", "package a\nfunc TestA() {}\n")
	f.check(f.cli("verify", "P001.1.1"), 0)
	f.check(f.cli("done", "P001.1.1", "--handoff=done"), 0)
	// The interrupted commit: the exact message docflow commit expects,
	// made by hand so the journal done wrote stays behind.
	f.git("add", "-A")
	c := exec.Command("git", "commit", "-q", "-F", "-")
	c.Dir = f.root
	c.Stdin = strings.NewReader("P001.1.1: first unit\n\n- done\n\nTest changes:\n- a_test.go: -1 lines\n")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	r := f.run(nil, "--max-units", "1")
	f.check(r, 0, "resuming pending commit", "(recovered commit)",
		"test change: a_test.go: -1 lines",
		", 1 test changes")
	var items []any
	for _, record := range fixtureEvents(t, f) {
		switch record["event"] {
		case "commit":
			items, _ = record["test_changes"].([]any)
		case "unit_end":
			if record["unit"] == "P001.1.1" && record["test_changes"] != float64(1) {
				t.Fatalf("unit_end test_changes = %v, want 1", record["test_changes"])
			}
		}
	}
	if len(items) != 1 || items[0] != "a_test.go: -1 lines" {
		t.Fatalf("commit event test_changes = %v:\n%s", items, r.out)
	}
}

// The replan rules live in two places (the P025 gotcha): the rule text the
// runner puts into the replan prompt and the replan chapter of the
// bsv-docflow-plan skill. The invariant phrases must stay in both.
func TestVerifyGuardReplanRulesSkillParity(t *testing.T) {
	text, err := os.ReadFile(filepath.Join("..", "..", "skills", "bsv-docflow-plan", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	skill := string(text)
	for _, phrase := range []string{"the runner skips a `[x]` unit", "`Role: review`", "`Role: acceptance`"} {
		if !strings.Contains(replanRules, phrase) {
			t.Errorf("the replan rule text lost %q", phrase)
		}
		if !strings.Contains(skill, phrase) {
			t.Errorf("skills/bsv-docflow-plan/SKILL.md lost %q", phrase)
		}
	}
	if !strings.Contains(skill, "Replan changed check") {
		t.Error("skills/bsv-docflow-plan/SKILL.md does not name the Replan changed check record")
	}
}

// The skills the binary installs carry the guard texts: install-skills from
// the built CLI puts the working-tree skills into a personal directory.
func TestVerifyGuardSkillsInstallCarriesGuardTexts(t *testing.T) {
	target := filepath.ToSlash(t.TempDir())
	c := exec.Command(runnerBinary(t), "install-skills", "--agent", "pi", "--target", target)
	c.Dir = t.TempDir()
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("install-skills: %v: %s", err, out)
	}
	for path, phrases := range map[string][]string{
		"bsv-docflow-plan/SKILL.md": {"Replan changed check", "the runner skips a `[x]` unit"},
		"bsv-docflow-run/SKILL.md":  {"test change:", "Test changes"},
	} {
		text, err := os.ReadFile(filepath.FromSlash(target + "/" + path))
		if err != nil {
			t.Fatalf("%s: %v (install output above)", path, err)
		}
		for _, phrase := range phrases {
			if !strings.Contains(string(text), phrase) {
				t.Errorf("installed %s lost %q", path, phrase)
			}
		}
	}
}
