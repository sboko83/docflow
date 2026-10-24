package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/format"
)

const replanCheckPlan = `---
id: P001
title: Plan
type: feature
status: active
created: 2026-10-12
---
## Goal

Ship it.

## Context

- docs/design/README.md

## Constraints

- stdlib only.

## Prerequisites

- Test account: ` + "`echo ok`" + `

## Traps

## Decisions

## Phase 1: First

### [x] P001.1.1 Done
Read:
- a.py
Do:
- b
Verify: ` + "`true`" + `
Commit: P001.1.1: done
Handoff:
- a.py exists

### [!] P001.1.2 Stuck
Read:
- b.py
Do:
- c
Verify: ` + "`true`" + `
Commit: P001.1.2: stuck
Handoff:
- Blocked (plan): b.py is gone

### [ ] P001.1.3 Todo
Read:
- a.py
Do:
- d
Verify: ` + "`true`" + `
Commit: P001.1.3: todo
`

func TestCheckReplan(t *testing.T) {
	lifted := strings.ReplaceAll(replanCheckPlan, "[!]", "[~]")
	for _, tc := range []struct{ name, before, after, want string }{
		{"clean", replanCheckPlan, lifted, ""},
		{"header", replanCheckPlan, strings.ReplaceAll(lifted, "title: Plan", "title: replanned"), "plan header changed"},
		{"permissions", replanCheckPlan, strings.ReplaceAll(lifted, "created: 2026-10-12", "created: 2026-10-12\npermissions: full"), "plan header changed"},
		{"goal", replanCheckPlan, strings.ReplaceAll(lifted, "Ship it.", "Ship it fast."), "section Goal changed"},
		{"constraints", replanCheckPlan, strings.ReplaceAll(lifted, "- stdlib only.", "- anything goes."), "section Constraints changed"},
		{"prerequisites", replanCheckPlan, strings.ReplaceAll(lifted, "echo ok", "echo fine"), "section Prerequisites changed"},
		{"prerequisites-removed", replanCheckPlan, strings.ReplaceAll(lifted, "- Test account: `echo ok`\n", ""), "section Prerequisites changed"},
		{"prerequisites-added", strings.ReplaceAll(replanCheckPlan, "## Prerequisites\n\n- Test account: `echo ok`\n\n", ""), lifted, "section Prerequisites changed"},
		{"done-text", replanCheckPlan, strings.ReplaceAll(lifted, "- a.py exists", "- a.py exists, changed"), "unit P001.1.1 [x] changed"},
		{"done-status", replanCheckPlan, strings.ReplaceAll(lifted, "[x]", "[ ]"), "unit P001.1.1 [x] changed"},
		{"done-removed", replanCheckPlan, lifted[:strings.Index(lifted, "### [x]")] + lifted[strings.Index(lifted, "### [~]"):], "unit P001.1.1 removed ([x])"},
		{"open-removed", replanCheckPlan, strings.ReplaceAll(lifted, "P001.1.3", "P001.1.9"), "unit P001.1.3 removed"},
		{"still-blocked", replanCheckPlan, strings.ReplaceAll(replanCheckPlan, "- b.py", "- a.py"), "unit P001.1.2 is still [!]"},
		{"cancelled", strings.ReplaceAll(replanCheckPlan, "[x]", "[-]"), strings.ReplaceAll(lifted, "[x]", "[ ]"), "unit P001.1.1 [-] changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := checkReplan(tc.before, tc.after, "P001.1.2")
			if strings.Join(got, "\n") != tc.want {
				t.Fatalf("got %v, want %q", got, tc.want)
			}
		})
	}
	after := strings.ReplaceAll(lifted, "- docs/design/README.md", "- docs/design/cli.md")
	extra := "### [ ] P001.1.4 Extra\nRead:\n- a.py\nDo:\n- e\nVerify: `true`\nCommit: P001.1.4: extra\n\n"
	after = strings.ReplaceAll(after, "### [ ] P001.1.3 Todo", extra+"### [ ] P001.1.3 Todo")
	after += "Handoff:\n- mid-session note\n"
	if got := checkReplan(replanCheckPlan, after, "P001.1.2"); len(got) != 0 {
		t.Fatal(got)
	}
	after = strings.ReplaceAll(after, "Commit: P001.1.4: extra\n", "")
	line := strings.Count(after[:strings.Index(after, "### [ ] P001.1.4")], "\n") + 1
	want := []string{fmt.Sprintf("lint: %d: unit P001.1.4: missing 'Commit'", line)}
	if got := checkReplan(replanCheckPlan, after, "P001.1.2"); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v != %v", got, want)
	}
	for _, pair := range [][2]string{{replanCheckPlan, "----\n" + replanCheckPlan}, {"broken", lifted}} {
		got := checkReplan(pair[0], pair[1], "P001.1.2")
		if len(got) != 1 || !strings.HasPrefix(got[0], "the plan no longer parses:") {
			t.Fatal(got)
		}
	}
	duplicate := lifted + "\n### [x] P001.1.1 Different history\nRead:\n- a.py\nDo:\n- b\nVerify: `true`\nCommit: P001.1.1: done\n"
	got := checkReplan(replanCheckPlan, duplicate, "P001.1.2")
	if len(got) == 0 || got[0] != "unit P001.1.1 [x] changed" {
		t.Fatal(got)
	}
}

func TestReplanPrompt(t *testing.T) {
	plan, err := format.ParsePlan(replanCheckPlan, planFile)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := replanPrompt(plan, "P001.1.2", "  P001.1.2: blocked (needs plan): b.py is gone\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"# Replan P001.1.2 Stuck\n", "Plan: P001 Plan (" + planFile + ")", "## Trigger\nP001.1.2: blocked (needs plan): b.py is gone\n\n", "## Handoff of P001.1.2", "- Blocked (plan): b.py is gone", "## Tree\nclean: no uncommitted work", "bsv-docflow-plan", "Replan mode", "Edit only the plan file", "new units with the next", "Context links", "Traps", "append to Decisions", "frontmatter", "Goal", "Constraints", "Prerequisites", "`[x]`", "`[-]`", "Do not do the unit", "docflow lint", "Do not commit", `"blocker"`, `"human"`} {
		if !strings.Contains(prompt, s) {
			t.Errorf("missing %q", s)
		}
	}
	prompt, err = replanPrompt(plan, "P001.1.2", planFile+":18: no such file: b.py", []string{" M src/a.py", "?? src/b.py"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "no such file: b.py") || !strings.Contains(prompt, "## Tree\n M src/a.py\n?? src/b.py\n") {
		t.Fatal(prompt)
	}
	if _, err := replanPrompt(plan, "P001.9.9", "x", nil); err == nil {
		t.Fatal("unknown unit accepted")
	}
	plan, err = format.ParsePlan(strings.ReplaceAll(replanCheckPlan, "- Blocked (plan): b.py is gone\n", "- Blocked (plan): b.py is gone\u00a0\u2003\n"), planFile)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err = replanPrompt(plan, "P001.1.2", "\u2003\x1cstale\x1f\u00a0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "## Trigger\nstale\n\n") || !strings.Contains(prompt, "- Blocked (plan): b.py is gone\n\n## Tree") {
		t.Fatal(prompt)
	}
}

func newReplanFixture(t *testing.T) *runFixture {
	f := newRunFixture(t)
	f.write("docs/docflow.md", strings.ReplaceAll(loopDoc, "- P002\n", ""))
	header := strings.ReplaceAll(loopHeader, "created: 2026-10-12", "created: 2026-10-20\nexecutor: claude | opus | high\nreplan: claude | haiku | low")
	f.write(planFile, header+"### [ ] P001.1.1 First unit\nRead:\n- a.txt\nDo:\n- write b.txt\nVerify: `test -f b.txt`\nCommit: P001.1.1: first unit\n\n### [ ] P001.1.2 Second unit\nRead:\n- c.txt\nDo:\n- write d.txt\nVerify: `test -f d.txt`\nCommit: P001.1.2: second unit\n")
	f.write("a.txt", "a\n")
	f.write("c.txt", "c\n")
	f.commit("fixture")
	fake, _ := filepath.Abs("../agentio/testdata/fake_agent.sh")
	bin := filepath.Join(f.tmp, "bin")
	wrapper := filepath.Join(bin, "claude")
	f.write(wrapper, "#!/bin/sh\ncase \"$*\" in *\"/usage\"*) exit 0 ;; esac\nexec /bin/bash "+shellQuote(fake)+" \"$@\"\n")
	if err := os.Chmod(wrapper, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return f
}

const blockedReason = "Do расходится с кодом"

func blockReply() string {
	return doneReply(map[string]any{"status": "blocked", "reason": blockedReason, "blocker": "plan"})
}
func liftReply(uid string) string { return editPlan("### [!] "+uid, "### [~] "+uid) + doneReply() }
func editPlan(old, new string) string {
	// awk preserves the fixture's newline bytes and allows literal multiline edits.
	return "REPLAN_OLD=" + shellQuote(old) + " REPLAN_NEW=" + shellQuote(new) + " awk 'BEGIN { old=ENVIRON[\"REPLAN_OLD\"]; new=ENVIRON[\"REPLAN_NEW\"]; RS=\"\\034\"; ORS=\"\" } { i=index($0,old); if(i) $0=substr($0,1,i-1) new substr($0,i+length(old)); print }' " + planFile + " > plan.tmp\nmv plan.tmp " + planFile + "\n"
}

func TestReplanRun(t *testing.T) {
	for _, mode := range []string{"success", "quota", "stale-read", "broken-link", "unchanged", "dirty-open", "dirty-resume", "other-file", "permissions", "agent-commit", "human", "replan-blocked", "unit-limit", "run-limit", "no-key", "disabled", "skills"} {
		t.Run(mode, func(t *testing.T) {
			f := newReplanFixture(t)
			steps := []string{blockReply(), liftReply("P001.1.1"), writeReply("b.txt"), writeReply("d.txt")}
			args, code, needle, calls := []string{}, 0, "P001: ready to close", 4
			switch mode {
			case "quota":
				for i := range steps {
					steps[i] = quotaEvent(.08, .07, 1792476000) + steps[i]
				}
			case "stale-read", "broken-link", "unchanged":
				steps = []string{"rm c.txt\n" + writeReply("b.txt"), editPlan("- c.txt", "- docs/docflow.md") + doneReply(), writeReply("d.txt")}
				calls = 3
				if mode != "stale-read" {
					code, calls = 3, 2
					if mode == "unchanged" {
						steps[1], needle = doneReply(), "the plan is unchanged"
					} else {
						steps[1], needle = editPlan("## Decisions\n", "## Decisions\n\n- c.txt stays\n")+doneReply(), "unit P001.1.2: c.txt: file not found"
					}
					steps = steps[:2]
				}
			case "dirty-open":
				steps = []string{"touch b.txt\n" + blockReply(), editPlan("### [!] P001.1.1", "### [ ] P001.1.1") + doneReply()}
				code, needle, calls = 3, "unit P001.1.1 is [ ] but the tree has uncommitted work", 2
			case "dirty-resume":
				steps[0] = "touch b.txt\n" + steps[0]
				steps[2] = doneReply()
			case "other-file":
				steps = []string{blockReply(), "echo x > x.txt\n" + liftReply("P001.1.1")}
				code, needle, calls = 3, "the tree changed outside the plan file", 2
			case "permissions":
				steps = []string{blockReply(), editPlan("status: active", "status: active\npermissions: full") + liftReply("P001.1.1")}
				code, needle, calls = 3, "plan header changed", 2
			case "agent-commit":
				steps = []string{blockReply(), "touch x.txt\ngit add x.txt\ngit commit -qm fake\n" + liftReply("P001.1.1")}
				code, needle, calls = 3, "HEAD moved during the replan", 2
			case "human":
				steps = []string{doneReply(map[string]any{"status": "blocked", "reason": "нужен вход", "blocker": "human"})}
				code, needle, calls = 3, "blocked (needs human): нужен вход", 1
			case "replan-blocked":
				steps = []string{blockReply(), doneReply(map[string]any{"status": "blocked", "reason": "без пользователя план не починить", "blocker": "human"})}
				code, needle, calls = 3, "replan blocked (needs human): без пользователя план не починить", 2
			case "unit-limit":
				steps = []string{blockReply(), liftReply("P001.1.1"), blockReply(), liftReply("P001.1.1"), blockReply()}
				code, needle, calls = 3, "P001.1.1: replan limit reached", 5
			case "run-limit":
				steps = []string{blockReply(), liftReply("P001.1.1"), blockReply()}
				args = []string{"--replans", "1"}
				code, needle, calls = 3, "P001.1.1: replan limit reached", 3
			case "no-key", "disabled":
				if mode == "no-key" {
					f.replace(planFile, "replan: claude | haiku | low\n", "")
				} else {
					args = []string{"--replans", "0"}
				}
				steps = []string{blockReply()}
				code, needle, calls = 3, "blocked (needs plan): "+blockedReason, 1
			case "skills":
				t.Setenv("DOCFLOW_SKILLS_CHECK", "1")
				steps = []string{writeReply("b.txt")}
				args = []string{"--max-units", "1"}
				needle, calls = "skills: claude: not installed bsv-changelog", 1
			}
			if len(steps) > 1 {
				steps[1] = "cp " + planFile + " .git/replan-before\n" + steps[1]
			}
			f.steps(steps...)
			r := f.cli(append([]string{"run", "--no-wait"}, args...)...)
			f.check(r, code, needle)
			wantTimes := map[string]int{"success": 4, "quota": 4, "human": 1, "replan-blocked": 2, "unchanged": 3}
			if want, ok := wantTimes[mode]; ok {
				if got := strings.Count(r.out, " time: "); got != want {
					t.Fatalf("duration rows %d != %d:\n%s", got, want, r.out)
				}
			}
			gotCalls := f.calls()
			if len(gotCalls) != calls {
				t.Fatalf("calls %d != %d\n%s%s", len(gotCalls), calls, r.out, r.err)
			}
			subjects := f.git("log", "--format=%s")
			if mode == "success" || mode == "dirty-resume" || mode == "quota" {
				if subjects != "P001.1.2: second unit\nP001.1.1: first unit\nP001: replan P001.1.1\nfixture\ninit" {
					t.Fatal(subjects)
				}
				if !strings.Contains(r.out, ", 1 replan") {
					t.Fatal(r.out)
				}
				for _, s := range []string{"# Replan P001.1.1", blockedReason, "Blocked (plan): " + blockedReason} {
					if !strings.Contains(gotCalls[1].Stdin, s) {
						t.Error(s)
					}
				}
				if strings.Contains(gotCalls[1].Stdin, "## Cycle") {
					t.Fatal("unit instructions in replan prompt")
				}
				argv := strings.Join(gotCalls[1].Argv, " ")
				if !strings.Contains(argv, "--model haiku") || !strings.Contains(argv, "low") {
					t.Fatal(argv)
				}
			}
			if mode == "quota" {
				var lines []string
				for _, line := range strings.Split(r.out, "\n") {
					if strings.HasPrefix(line, "quota: ") {
						lines = append(lines, line)
					}
				}
				want := []string{"quota: claude 5h 8% · 7d 7%", "quota: claude 5h 8% (+0) · 7d 7% (+0)", "quota: claude 5h 8% (+0) · 7d 7% (+0)", "quota: claude 5h 8% (+0) · 7d 7% (+0)"}
				if !reflect.DeepEqual(lines, want) {
					t.Fatalf("%v != %v", lines, want)
				}
			}
			if mode == "dirty-open" && !strings.Contains(gotCalls[1].Stdin, "?? b.txt") {
				t.Fatal(gotCalls[1].Stdin)
			}
			if mode == "other-file" {
				if _, err := os.Stat(filepath.Join(f.root, "x.txt")); err != nil {
					t.Fatal(err)
				}
			}
			if code == 3 && mode != "unit-limit" && mode != "run-limit" && mode != "stale-read" && mode != "broken-link" && mode != "unchanged" {
				if !strings.Contains(f.read(planFile), "### [!] P001.1.1") || strings.Contains(f.read(planFile), "permissions: full") {
					t.Fatal(f.read(planFile))
				}
				if strings.Contains(subjects, "P001: replan") {
					t.Fatal(subjects)
				}
			}
			if code == 3 && calls == 2 && f.read(planFile) != f.read(".git/replan-before") {
				t.Fatal("plan was not restored byte-for-byte")
			}
		})
	}
}

func TestRestorePlan(t *testing.T) {
	for _, text := range []string{"snapshot\n", "snapshot\r\nwithout final newline"} {
		path := filepath.Join(t.TempDir(), "plan.md")
		if err := restorePlan(path, text); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(1000000000, 0)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if err := restorePlan(path, text); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(stamp) {
			t.Fatal("unchanged snapshot was rewritten")
		}
		if err := os.WriteFile(path, []byte("changed"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := restorePlan(path, text); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != text {
			t.Fatalf("restored %q: %v", got, err)
		}
	}
}

func TestReplanAgentFailures(t *testing.T) {
	for _, mode := range []string{"retry", "no-session", "error-result", "auth", "limit", "exhausted", "blocked-edit", "commit-hook"} {
		t.Run(mode, func(t *testing.T) {
			f := newReplanFixture(t)
			snapshot := "cp " + planFile + " .git/replan-before\n"
			steps := []string{blockReply(), snapshot + "echo broken\nexit 1\n", liftReply("P001.1.1"), writeReply("b.txt"), writeReply("d.txt")}
			code, needle := 0, "P001: ready to close"
			args := []string{"run", "--no-wait"}
			switch mode {
			case "retry":
				steps[1] = snapshot + editPlan("## Decisions\n", "## Decisions\n\n- retry note\n") + emit(map[string]any{"type": "result", "is_error": true, "session_id": "r1", "result": "retry me"})
			case "error-result":
				steps[1] = snapshot + doneReply(map[string]any{"handoff": []string{}})
			case "auth":
				steps = []string{blockReply(), snapshot + emit(map[string]any{"type": "result", "is_error": true, "result": "Failed to authenticate"})}
				code, needle = 7, "claude is not logged in"
			case "limit":
				steps = []string{blockReply(), snapshot + emit(map[string]any{"type": "result", "is_error": true, "result": "usage limit reached"})}
				code, needle = 7, "replan usage limit (--no-wait)"
			case "exhausted":
				steps = []string{blockReply(), snapshot + editPlan("title: Plan", "title: broken") + "exit 9\n"}
				args = append(args, "--attempts", "1")
				code, needle = 7, "replan gave up after 1 attempts"
			case "blocked-edit":
				steps = []string{blockReply(), snapshot + editPlan("title: Plan", "title: broken") + doneReply(map[string]any{"status": "blocked", "reason": "need help", "blocker": "human"})}
				code, needle = 3, "replan blocked (needs human): need help"
			case "commit-hook":
				f.write(".git/hooks/pre-commit", "#!/bin/sh\necho denied >&2\nexit 1\n")
				if err := os.Chmod(filepath.Join(f.root, ".git/hooks/pre-commit"), 0o755); err != nil {
					t.Fatal(err)
				}
				steps = []string{blockReply(), snapshot + liftReply("P001.1.1")}
				code, needle = 4, "replan commit failed"
			}
			f.steps(steps...)
			r := f.cli(args...)
			f.check(r, code, needle)
			if code != 0 && mode != "commit-hook" && f.read(planFile) != f.read(".git/replan-before") {
				t.Fatal("failure did not restore snapshot")
			}
			if code == 0 {
				calls := f.calls()
				if len(calls) != 5 {
					t.Fatal(calls)
				}
				if !strings.Contains(calls[2].Stdin, "## Previous attempt") && mode == "no-session" {
					t.Fatal(calls[2])
				}
				if mode == "retry" && (!strings.Contains(strings.Join(calls[2].Argv, " "), "--resume r1") || !strings.Contains(calls[2].Stdin, "retry me")) {
					t.Fatal(calls[2])
				}
			}
		})
	}
}

func TestReplanTimeout(t *testing.T) {
	f := newReplanFixture(t)
	f.replace(planFile, "### [ ] P001.1.1", "### [!] P001.1.1")
	before := f.read(planFile)
	f.steps(editPlan("title: Plan", "title: changed") + "sleep 2\n")
	config, err := agents.LoadAgents("", false)
	if err != nil {
		t.Fatal(err)
	}
	profile(config, "claude")["timeout"] = .1
	var out, errOut bytes.Buffer
	p := newProgress(&out, &errOut, false)
	defer p.stop()
	totals := &runTotals{quota: map[string]agentio.Quota{}, quotaStart: map[string]agentio.Quota{}}
	code, err := replanUnit(context.Background(), f.root, Args{Attempts: 1}, config, "P001.1.1", "stale", p, totals)
	if err != nil {
		t.Fatal(err)
	}
	f.check(runResult{code, out.String(), errOut.String()}, 7, "timed out after 0.1 s", "the plan is restored")
	if f.read(planFile) != before {
		t.Fatal("timeout did not restore plan")
	}
}

func TestReplanPlanTimeout(t *testing.T) {
	f := newReplanFixture(t)
	f.replace(planFile, "created: 2026-10-20\n", "created: 2026-10-20\ntimeout: 60\n")
	f.replace(planFile, "### [ ] P001.1.1", "### [!] P001.1.1")
	f.steps("sleep 1.1\n" + liftReply("P001.1.1"))
	config, err := agents.LoadAgents("", false)
	if err != nil {
		t.Fatal(err)
	}
	profile(config, "claude")["timeout"] = 1
	var out, errOut bytes.Buffer
	p := newProgress(&out, &errOut, false)
	defer p.stop()
	totals := &runTotals{quota: map[string]agentio.Quota{}, quotaStart: map[string]agentio.Quota{}}
	code, err := replanUnit(context.Background(), f.root, Args{Attempts: 1}, config, "P001.1.1", "stale", p, totals)
	if err != nil {
		t.Fatal(err)
	}
	f.check(runResult{code, out.String(), errOut.String()}, 0, "P001.1.1 replanned:")
}
