//go:build darwin || linux

package runner

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Acceptance of P041: a unit blocked with `needs human` resumes after the
// person has done the step. Every scenario goes through the built CLI on the
// fixture plan with the fake agent. The real-TTY scenarios — the question at
// the start of a run and Ctrl-C at it — are TestTerminalUnblockAtRunStart and
// TestTerminalUnblockSignalCancels.

const (
	runHint       = `unblock: docflow unblock P001.1.1 --note "…", then docflow run`
	statusHint    = `unblock: docflow unblock P001.1.1 --note "…"`
	recheckRule   = "check that condition with a command first"
	legacyRecheck = "a legacy `Blocked:` item counts as human"
)

// cliFree runs the CLI as the person does, outside any runner.
func (f *runFixture) cliFree(args ...string) runResult {
	f.t.Helper()
	f.t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	return f.cli(args...)
}

// promptText joins the prompt's words: the cycle text wraps mid-phrase.
func promptText(call fakeCall) string { return strings.Join(strings.Fields(call.Stdin), " ") }

func humanBlocked(reason string) string {
	return envelope(map[string]any{"status": "blocked", "blocker": "human", "reason": reason})
}

func TestAcceptHumanBlockToCommit(t *testing.T) {
	f := newRunFixture(t)
	r := f.run([]string{humanBlocked("нужен вход в аккаунт")}, "--max-units", "1")
	f.check(r, 3, "blocked (needs human): нужен вход в аккаунт", runHint)
	if plan := f.read(planFile); !strings.Contains(plan, "[!] P001.1.1") || !strings.Contains(plan, "- Blocked (human): нужен вход в аккаунт\n") {
		t.Fatal(plan)
	}

	st := f.cliFree("status")
	f.check(st, 0, "blocked: P001.1.1 First unit (needs human)", statusHint)

	u := f.cliFree("unblock", "P001.1.1", "--note", "x")
	f.check(u, 0)
	plan := f.read(planFile)
	if !strings.Contains(plan, "[~] P001.1.1") || !strings.Contains(plan, "- Unblocked: x\n") {
		t.Fatal(plan)
	}
	if head := f.git("log", "-1", "--format=%s"); strings.HasPrefix(head, "P001.1.1") {
		t.Fatalf("unblock committed: %s", head)
	}

	r = f.run([]string{writeReply("a.txt")}, "--max-units", "1")
	f.check(r, 0, "P001.1.1 done:")
	calls := f.calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	prompt := promptText(calls[0])
	for _, s := range []string{"P001.1.1 First unit", "Blocked (human): нужен вход в аккаунт", "Unblocked: x", recheckRule} {
		if !strings.Contains(prompt, s) {
			t.Errorf("prompt misses %q:\n%s", s, calls[0].Stdin)
		}
	}
	if plan := f.read(planFile); !strings.Contains(plan, "[x] P001.1.1") {
		t.Fatal(plan)
	}
	if head := f.git("log", "-1", "--format=%s"); head != "P001.1.1: first unit" {
		t.Fatalf("last commit = %q", head)
	}
	if status := f.git("status", "--porcelain"); status != "" {
		t.Fatalf("dirty tree after the commit:\n%s", status)
	}
}

func TestAcceptUnblockRefusesPlanBlock(t *testing.T) {
	f := newRunFixture(t)
	f.check(f.cliFree("block", "P001.1.1", "--needs", "plan", "--reason", "Do устарел"), 0)
	f.commit("block")
	before := f.read(planFile)
	f.check(f.cliFree("status"), 0, "blocked: P001.1.1 First unit (needs plan)")
	r := f.cliFree("unblock", "P001.1.1", "--note", "x")
	f.check(r, 5, "P001.1.1 is blocked (needs plan): replan it with bsv-docflow-plan")
	if plan := f.read(planFile); plan != before {
		t.Fatalf("the plan changed:\n%s", plan)
	}
	if status := f.git("status", "--porcelain"); status != "" {
		t.Fatalf("unblock touched the tree:\n%s", status)
	}
}

// TestAcceptLegacyBlockRecheck: a legacy `Blocked: …` item is a human block.
// The fake agent stands for the rule of the cycle text: it checks the blocked
// condition, a file outside the repository, before it does the work.
func TestAcceptLegacyBlockRecheck(t *testing.T) {
	for _, met := range []bool{false, true} {
		name := map[bool]string{false: "unmet", true: "met"}[met]
		t.Run(name, func(t *testing.T) {
			f := newRunFixture(t)
			f.replace(planFile, "[ ] P001.1.1", "[!] P001.1.1")
			f.replace(planFile, "Commit: P001.1.1: first unit\n", "Commit: P001.1.1: first unit\nHandoff:\n- Blocked: нужен вход в аккаунт\n")
			f.check(f.cliFree("status"), 0, "(needs human)", statusHint)
			f.check(f.cliFree("unblock", "P001.1.1", "--note", "вошёл"), 0)
			if plan := f.read(planFile); !strings.Contains(plan, "[~] P001.1.1") || !strings.Contains(plan, "- Unblocked: вошёл\n") {
				t.Fatal(plan)
			}
			condition := filepath.Join(f.tmp, "logged-in")
			if met {
				f.write(condition, "ok\n")
			}
			step := "if [ -f " + shellQuote(condition) + " ]; then\n" + writeReply("a.txt") +
				"else\n" + humanBlocked("вход в аккаунт всё ещё не выполнен") + "fi\n"
			r := f.run([]string{step}, "--max-units", "1")
			calls := f.calls()
			if len(calls) != 1 {
				t.Fatalf("calls = %d:\n%s%s", len(calls), r.out, r.err)
			}
			prompt := promptText(calls[0])
			for _, s := range []string{"P001.1.1 First unit", "Blocked: нужен вход в аккаунт", "Unblocked: вошёл", recheckRule, legacyRecheck} {
				if !strings.Contains(prompt, s) {
					t.Errorf("prompt misses %q:\n%s", s, calls[0].Stdin)
				}
			}
			plan := f.read(planFile)
			_, statErr := os.Stat(filepath.Join(f.root, "a.txt"))
			if !met {
				f.check(r, 3, "blocked (needs human): вход в аккаунт всё ещё не выполнен", runHint)
				if !strings.Contains(plan, "[!] P001.1.1") || !strings.HasSuffix(strings.SplitN(plan, "### [ ] P001.1.2", 2)[0], "- Blocked (human): вход в аккаунт всё ещё не выполнен\n\n") {
					t.Fatal(plan)
				}
				if statErr == nil {
					t.Fatal("the agent did the remaining work on an unmet condition")
				}
				return
			}
			f.check(r, 0, "P001.1.1 done:")
			if !strings.Contains(plan, "[x] P001.1.1") || statErr != nil {
				t.Fatalf("%v\n%s", statErr, plan)
			}
			if head := f.git("log", "-1", "--format=%s"); head != "P001.1.1: first unit" {
				t.Fatalf("last commit = %q", head)
			}
		})
	}
}

// TestAcceptPlanBlockLookalikes: Handoff text that only looks like a block,
// after a real plan block, neither hides it nor lets anyone lift it.
func TestAcceptPlanBlockLookalikes(t *testing.T) {
	f := newRunFixture(t)
	f.replace(planFile, "[ ] P001.1.1", "[!] P001.1.1")
	f.replace(planFile, "Commit: P001.1.1: first unit\n", "Commit: P001.1.1: first unit\nHandoff:\n- Blocked (plan): Do устарел\n- Blocked (plan)\n- Blocked (device): not a class\n- Blockedness: nothing to see\n- blocked (human): lowercase\n")
	before := f.read(planFile)
	st := f.cliFree("status")
	f.check(st, 0, "blocked: P001.1.1 First unit (needs plan)")
	if strings.Contains(st.out, "needs human") || strings.Contains(st.out, "unblock:") {
		t.Fatal(st.out)
	}
	f.check(f.cliFree("unblock", "P001.1.1", "--note", "x"), 5, "(needs plan)")
	if plan := f.read(planFile); plan != before {
		t.Fatalf("the plan changed:\n%s", plan)
	}
	f.steps(writeReply("a.txt"))
	code, out, tty := runInTerminalWithInput(t, f, "y\nзаметка\n", "--max-units", "1")
	if code != 3 || !strings.Contains(out, "P001.1.1 is blocked") {
		t.Fatalf("exit %d:\n%s%s", code, out, tty)
	}
	if strings.Contains(out, "done?") || strings.Contains(out, "unblock:") {
		t.Fatalf("the run offered a human unblock:\n%s", out)
	}
	if plan := f.read(planFile); plan != before || len(f.calls()) != 0 {
		t.Fatalf("calls %d, plan:\n%s", len(f.calls()), plan)
	}
}

// TestAcceptHelpListsUnblock: the built CLI lists `unblock` between `block`
// and `lint` in both root help forms, and every listed command has a help.
func TestAcceptHelpListsUnblock(t *testing.T) {
	f := newRunFixture(t)
	item := regexp.MustCompile(`(?m)^    (\S+)  `)
	for _, flag := range []string{"-h", "--help"} {
		r := f.cliFree(flag)
		f.check(r, 0)
		section := r.out
		if i := strings.Index(section, "positional arguments:"); i >= 0 {
			section = section[i:]
		}
		if i := strings.Index(section, "options:"); i >= 0 {
			section = section[:i]
		}
		names := []string{}
		for _, m := range item.FindAllStringSubmatch(section, -1) {
			names = append(names, m[1])
		}
		at := map[string]int{}
		for i, n := range names {
			at[n] = i
		}
		b, okB := at["block"]
		u, okU := at["unblock"]
		l, okL := at["lint"]
		if !okB || !okU || !okL || u != b+1 || l != u+1 || strings.Count(section, "\n    unblock ") != 1 {
			t.Fatalf("%s: commands %v", flag, names)
		}
		if flag != "-h" {
			continue
		}
		for _, n := range names {
			if h := f.cliFree(n, "-h"); h.code != 0 || !strings.Contains(h.out, n) {
				t.Errorf("%s -h: exit %d\n%s%s", n, h.code, h.out, h.err)
			}
		}
	}
	h := f.cliFree("unblock", "-h")
	f.check(h, 0, "--note")
	if !strings.Contains(strings.ToLower(h.out), "block") {
		t.Fatal(h.out)
	}
}
