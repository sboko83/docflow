package runner

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The fake invokes docflow by name, exercising the closing call's PATH.
const closeArchiveStep = `docflow close P001
archive=docs/archive/P001-plan.md
awk '{ print; if ($0 ~ /^## /) print "\nNone.\n" }' "$archive" > "$HOME/filled-archive"
mv "$HOME/filled-archive" "$archive"
`

func readyCloseFixture(t *testing.T) *runFixture {
	t.Helper()
	f := newRunFixture(t)
	f.replace(planFile, "### [ ]", "### [x]")
	return f
}

func TestRunCloseAfterUnits(t *testing.T) {
	f := newRunFixture(t)
	r := f.run([]string{writeReply("a.txt"), writeReply("b.txt"), closeArchiveStep + doneReply()}, "--close")
	f.check(r, 0, "P001: closed", "run: 2 units,")
	if subject := f.git("log", "-1", "--format=%s"); subject != "P001: close plan" {
		t.Fatalf("closing commit = %q", subject)
	}
	if _, err := os.Stat(filepath.Join(f.root, planFile)); !os.IsNotExist(err) {
		t.Fatalf("plan still exists: %v", err)
	}
	if !strings.Contains(f.read("docs/archive/P001-plan.md"), "None.") || f.git("status", "--porcelain") != "" {
		t.Fatal("closing left an unfilled archive or a dirty tree")
	}
	if len(f.calls()) != 3 || !strings.Contains(f.read("docs/docflow.md"), "- P002") || strings.Contains(f.read("docs/docflow.md"), "- P001") {
		t.Fatal("closing crossed the plan boundary or did not update Queue")
	}
	records := fixtureEvents(t, f)
	found := false
	for _, record := range records {
		if record["event"] == "close" {
			found = true
			if record["plan"] != "P001" || record["result"] != "done" || record["exit_code"] != float64(0) {
				t.Fatalf("close event = %v", record)
			}
		}
	}
	if !found {
		t.Fatal("missing close event")
	}
	if _, err := os.Stat(filepath.Join(f.root, ".git/docflow/run/P001.close1.out")); err != nil {
		t.Fatal(err)
	}
	prompt := f.calls()[2].Stdin
	for _, instruction := range []string{"bsv-docflow-close", "docflow close P001", "Outcome", "Follow-ups", "Do not commit", `"blocker":""`} {
		if !strings.Contains(prompt, instruction) {
			t.Errorf("close prompt missing %q", instruction)
		}
	}
}

func TestRunCloseReadyAtStart(t *testing.T) {
	f := readyCloseFixture(t)
	// A hostile inherited docflow must not replace the running CLI for close.
	bin := filepath.Join(f.tmp, "bin")
	f.write(filepath.Join(bin, "docflow"), "#!/bin/sh\nexit 91\n")
	if err := os.Chmod(filepath.Join(bin, "docflow"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := f.run([]string{closeArchiveStep + doneReply()}, "--close")
	f.check(r, 0, "P001: closed", "run: 0 units,")
	if len(f.calls()) != 1 || f.git("log", "-1", "--format=%s") != "P001: close plan" {
		t.Fatal("ready plan was skipped")
	}
}

func TestRunCloseChecksLeaveTree(t *testing.T) {
	for _, tc := range []struct{ name, step, want string }{
		{"source", closeArchiveStep + "printf changed > source.go\n", "changed path outside closing scope: source.go"},
		{"staged source", closeArchiveStep + "printf changed > source.go\ngit add source.go\n", "changed path outside closing scope: source.go"},
		{"plan remains", "", "plan file still exists"},
		{"archive missing", "docflow close P001\nrm docs/archive/P001-plan.md\n", "archive entry missing"},
		{"lint", "docflow close P001\n", "docflow lint failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := readyCloseFixture(t)
			head := f.git("rev-parse", "HEAD")
			r := f.run([]string{tc.step + doneReply()}, "--close")
			f.check(r, 2, tc.want)
			if f.git("rev-parse", "HEAD") != head {
				t.Fatal("failed close committed changes")
			}
			if strings.Contains(tc.name, "source") && f.read("source.go") != "changed" {
				t.Fatal("source edit was rolled back")
			}
			records := fixtureEvents(t, f)
			if record := records[len(records)-2]; record["event"] != "close" || record["exit_code"] != float64(2) {
				t.Fatalf("failed close event = %v", record)
			}
		})
	}
}

func TestRunCloseRoutes(t *testing.T) {
	for _, cheap := range []bool{false, true} {
		name := "executor"
		if cheap {
			name = "cheap"
		}
		t.Run(name, func(t *testing.T) {
			f := executorRunFixture(t)
			f.replace(planFile, "### [ ]", "### [x]")
			f.replace(planFile, "status: active", "status: active\nfallback: codex | gpt-5.5 | -")
			want := "claude-opus-5-5"
			if cheap {
				f.replace(planFile, "status: active", "status: active\ncheap: claude | claude-haiku-5-5 | -")
				want = "claude-haiku-5-5"
			}
			f.steps(closeArchiveStep + doneReply())
			f.check(f.cli("run", "--close", "--no-wait"), 0, "P001: closed")
			calls := f.calls()
			if len(calls) != 1 || !strings.Contains(strings.Join(calls[0].Argv, " "), want) {
				t.Fatalf("closing routes = %v", calls)
			}
		})
	}
}

func TestRunCloseFallback(t *testing.T) {
	for _, cheap := range []bool{false, true} {
		name := "executor"
		if cheap {
			name = "cheap"
		}
		t.Run(name, func(t *testing.T) { testRunCloseFallback(t, cheap) })
	}
}

func testRunCloseFallback(t *testing.T, cheap bool) {
	t.Helper()
	f := executorRunFixture(t)
	f.replace(planFile, "### [ ]", "### [x]")
	if cheap {
		f.replace(planFile, "status: active", "status: active\ncheap: claude | claude-haiku-5-5 | -; codex | gpt-5.5 | -")
	} else {
		f.replace(planFile, "status: active", "status: active\nfallback: codex | gpt-5.5 | -")
	}
	refusal := emit(map[string]any{"type": "result", "is_error": true, "result": "API Error: 429 rate_limit_error", "session_id": "refused-session"})
	result := emit(map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": `{"status":"done","handoff":["closed"]}`}})
	f.steps(refusal, closeArchiveStep+result)
	f.check(f.cli("run", "--close", "--no-wait", "--attempts", "1"), 0, "P001: closed")
	calls := f.calls()
	if len(calls) != 2 || !strings.Contains(strings.Join(calls[1].Argv, " "), "gpt-5.5") || strings.Contains(strings.Join(calls[1].Argv, " "), "refused-session") {
		t.Fatalf("fallback calls = %v", calls)
	}
	if !strings.Contains(calls[1].Stdin, "## Previous attempt") {
		t.Fatal("fallback lost closing context")
	}
	for _, n := range []string{"1", "2"} {
		if _, err := os.Stat(filepath.Join(f.root, ".git/docflow/run/P001.close"+n+".out")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunCloseRefusalExhausted(t *testing.T) {
	f := executorRunFixture(t)
	f.replace(planFile, "### [ ]", "### [x]")
	// cheap is the entire route: the header fallback must not be appended.
	f.replace(planFile, "status: active", "status: active\ncheap: claude | claude-haiku-5-5 | -\nfallback: codex | gpt-5.5 | -")
	f.steps(emit(map[string]any{"type": "result", "is_error": true, "result": "API Error: 429 rate_limit_error"}))
	head := f.git("rev-parse", "HEAD")
	f.check(f.cli("run", "--close", "--no-wait"), 7, "all close executors unavailable")
	if len(f.calls()) != 1 || f.git("rev-parse", "HEAD") != head || f.git("status", "--porcelain") != "" {
		t.Fatal("exhausted cheap chain used a foreign fallback or changed the tree")
	}
}

func TestRunCloseRetryKeepsLogs(t *testing.T) {
	f := readyCloseFixture(t)
	f.write(".git/docflow/run/P001.close4.out", "previous run")
	f.check(f.run([]string{"printf 'missing result JSON\\n'\n", closeArchiveStep + doneReply()}, "--close", "--attempts", "2"), 0, "P001: closed")
	if f.read(".git/docflow/run/P001.close4.out") != "previous run" {
		t.Fatal("prior closing log was overwritten")
	}
	for _, n := range []string{"5", "6"} {
		if _, err := os.Stat(filepath.Join(f.root, ".git/docflow/run/P001.close"+n+".out")); err != nil {
			t.Fatal(err)
		}
	}
}

// TestRunCloseTimeCapWhileActive: the cap ended a closing call that kept
// speaking. The attempt is work cut short: a ↻ line, the session is resumed and
// the summary hints, the closing is not reported as a problem.
func TestRunCloseTimeCapWhileActive(t *testing.T) {
	f := readyCloseFixture(t)
	f.configure("stream = \"claude\"\ntimeout = 4\n")
	tick := emit(map[string]any{"type": "assistant", "message": map[string]any{
		"content": []any{map[string]any{"type": "text", "text": "tick"}},
	}})
	// The stream's last event is the result Claude reads the session id from:
	// it arrives while the call runs and stays fresh under the cap.
	step := emit(map[string]any{"type": "system", "subtype": "init", "session_id": "s1"}) + tick +
		"sleep 1\n" + tick + "sleep 1\n" +
		emit(map[string]any{"type": "result", "is_error": false, "result": "working", "session_id": "s1"}) +
		"sleep 60\n"
	r := f.run([]string{step, closeArchiveStep + doneReply()}, "--close", "--attempts", "2")
	f.check(r, 0, "P001: closed")
	text := r.out + r.err
	if !regexp.MustCompile(`↻ close attempt 1: time cap 4s reached while active \(\d+ turns\); resuming the session`).MatchString(text) {
		t.Errorf("no active-cap line for the closing call:\n%s", text)
	}
	if strings.Contains(text, "close agent timed out") {
		t.Errorf("an active closing call is reported as a problem:\n%s", text)
	}
	if !regexp.MustCompile(`hint: P001 needs more than 4s on fake -: set timeout: in the plan header`).MatchString(text) {
		t.Errorf("the run summary carries no cap hint:\n%s", text)
	}
	calls := f.calls()
	if len(calls) != 2 || !strings.Contains(strings.Join(calls[1].Argv, " "), "--resume s1") {
		t.Fatalf("closing calls = %v\n%s%s", calls, r.out, r.err)
	}
}

func TestRunCloseTimeCapDuringToolNamesTheActivity(t *testing.T) {
	f := readyCloseFixture(t)
	f.configure("stream = \"claude\"\ntimeout = 2\n")
	r := f.run([]string{toolStep(), closeArchiveStep + doneReply()}, "--close", "--attempts", "2")
	f.check(r, 0, "P001: closed",
		"↻ close attempt 1: time cap 2s reached during Bash go test ./... (1 turn); resuming the session")
}

// A closing call that printed nothing keeps the problem line and no cap hint.
func TestRunCloseSilentTimeoutStaysAProblem(t *testing.T) {
	f := readyCloseFixture(t)
	f.configure("timeout = 2\n")
	r := f.run([]string{"sleep 60\n"}, "--close", "--attempts", "1")
	f.check(r, 7, "P001: close: close agent timed out")
	if text := r.out + r.err; strings.Contains(text, "↻") || strings.Contains(text, "hint:") {
		t.Errorf("a silent closing call reads as active work:\n%s", text)
	}
}

func TestRunCloseDirtyTreeDoesNotCallAgent(t *testing.T) {
	f := readyCloseFixture(t)
	f.write("README.md", "existing work")
	f.check(f.run(nil, "--close"), 2, "close needs a clean working tree")
	if len(f.calls()) != 0 || f.read("README.md") != "existing work" {
		t.Fatal("dirty tree was changed or sent to the close agent")
	}
}

func TestRunCloseDryRun(t *testing.T) {
	for _, ready := range []bool{false, true} {
		for _, all := range []bool{false, true} {
			f := executorRunFixture(t)
			f.replace(planFile, "status: active", "status: active\ncheap: claude | claude-haiku-5-5 | -; codex | gpt-5.5 | -")
			if ready {
				f.replace(planFile, "### [ ]", "### [x]")
			}
			args := []string{"run", "--dry-run", "--close"}
			if all {
				args = append(args, "--all")
			}
			f.check(f.cli(args...), 0, "close: claude | claude-haiku-5-5 | -; codex | gpt-5.5 | -")
			if len(f.calls()) != 0 || f.git("status", "--porcelain") != "" {
				t.Fatal("dry-run called the agent or changed the tree")
			}
		}
	}
}

func TestRunWithoutCloseLeavesReadyPlan(t *testing.T) {
	f := newRunFixture(t)
	f.check(f.run([]string{writeReply("a.txt"), writeReply("b.txt")}), 0, "P001: ready to close")
	if len(f.calls()) != 2 || !strings.Contains(f.read(planFile), "### [x]") {
		t.Fatal("run without --close attempted closing")
	}
}

func TestRunCloseRespectsStopAndUnitLimit(t *testing.T) {
	for _, softStop := range []bool{false, true} {
		f := newRunFixture(t)
		steps := []string{writeReply("a.txt")}
		args := []string{"--close", "--max-units", "1"}
		calls := 1
		if softStop {
			steps = append(steps, "touch .git/docflow/run/stop\n"+writeReply("b.txt"))
			args = []string{"--close"}
			calls = 2
		}
		f.check(f.run(steps, args...), 0)
		if len(f.calls()) != calls {
			t.Fatal("stop or unit limit started the closing agent")
		}
		if _, err := os.Stat(filepath.Join(f.root, planFile)); err != nil {
			t.Fatal("stop or unit limit removed the plan")
		}
	}
}
