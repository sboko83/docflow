package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"bsv-docflow/internal/format"
)

const loopDoc = "---\ndocflow: 3\nread_budget: 40000\n---\n## Queue\n- P001\n- P002\n\n## Backlog\n"
const loopHeader = "---\nid: P001\ntitle: Plan\ntype: feature\nstatus: active\ncreated: 2026-10-12\n---\n## Goal\nЦель {model}.\n\n## Context\n\n## Constraints\n\n## Traps\n\n## Decisions\n\n## Phase 1: First\n\n"
const loopUnit = "### [ ] P001.1.%d %s unit\nRead:\n- docs/docflow.md\nDo:\n- write %s\nVerify: `test -f %s`\nCommit: P001.1.%d: %s unit\n\n"
const planFile = "docs/plans/P001-plan.md"

var buildOnce sync.Once
var testBinary, buildError string

func TestMain(m *testing.M) {
	code := m.Run()
	if testBinary != "" {
		_ = os.RemoveAll(filepath.Dir(testBinary))
	}
	os.Exit(code)
}

func runnerBinary(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell integration fixtures require Unix")
	}
	buildOnce.Do(func() {
		source, _ := filepath.Abs("../..")
		dir, err := os.MkdirTemp("", "docflow-run-tests-")
		if err != nil {
			buildError = err.Error()
			return
		}
		// The CLI's editable skills live beside the executable checkout.
		_ = os.Symlink(filepath.Join(source, "skills"), filepath.Join(dir, "skills"))
		testBinary = filepath.Join(dir, "docflow")
		cmd := exec.Command("go", "build", "-o", testBinary, "./cmd/docflow")
		cmd.Dir = source
		if b, err := cmd.CombinedOutput(); err != nil {
			buildError = fmt.Sprintf("%v: %s", err, b)
		}
	})
	if buildError != "" {
		t.Fatal(buildError)
	}
	return testBinary
}

type runFixture struct {
	t                                   *testing.T
	root, tmp, scenario, config, binary string
}
type runResult struct {
	code     int
	out, err string
}
type fakeCall struct {
	Argv  []string `json:"argv"`
	Stdin string   `json:"stdin"`
}

func newRunFixture(t *testing.T) *runFixture {
	t.Helper()
	f := &runFixture{t: t, root: t.TempDir(), tmp: t.TempDir(), binary: runnerBinary(t)}
	t.Setenv("DOCFLOW_NOTIFY", "0")
	t.Setenv("DOCFLOW_SKILLS_CHECK", "0")
	t.Setenv("DOCFLOW_AGENTS", "")
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	t.Setenv("HOME", f.tmp)
	t.Setenv("USERPROFILE", f.tmp)
	t.Setenv("XDG_CONFIG_HOME", f.tmp)
	t.Setenv("CODEX_HOME", filepath.Join(f.tmp, "codex"))
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	f.scenario = filepath.Join(f.tmp, "steps")
	f.config = filepath.Join(f.tmp, "agents.toml")
	_ = os.MkdirAll(f.scenario, 0o755)
	t.Setenv("FAKE_AGENT_SCENARIO", f.scenario)
	f.write("docs/docflow.md", loopDoc)
	plan := loopHeader + fmt.Sprintf(loopUnit, 1, "First", "a.txt", "a.txt", 1, "first") + fmt.Sprintf(loopUnit, 2, "Second", "b.txt", "b.txt", 2, "second")
	f.write(planFile, plan)
	f.write("docs/plans/P002-other.md", strings.ReplaceAll(loopHeader+fmt.Sprintf(loopUnit, 1, "Other", "c.txt", "c.txt", 1, "other"), "P001", "P002"))
	f.git("init", "-q")
	f.git("config", "user.name", "t")
	f.git("config", "user.email", "t@example.invalid")
	f.git("config", "commit.gpgsign", "false")
	f.commit("init")
	fake, _ := filepath.Abs("../agentio/testdata/fake_agent.sh")
	f.setConfig("[tiers]\nfast = {agent = \"fake\", model = \"fast\"}\ndefault = {agent = \"fake\", model = \"\"}\nstrong = {agent = \"fake\", model = \"strong\"}\n[agents.fake]\ncmd = [\"/bin/bash\", " + jsonString(fake) + "]\nmodel_args = [\"--model\", \"{model}\"]\nresume_args = [\"--resume\", \"{session}\"]\nresult = \"json:structured_output\"\ntext = \"json:result\"\nsession = \"json:session_id\"\n")
	return f
}

func (f *runFixture) write(path, text string) {
	f.t.Helper()
	full := path
	if !filepath.IsAbs(path) {
		full = filepath.Join(f.root, path)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
		f.t.Fatal(err)
	}
}
func (f *runFixture) read(path string) string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, path))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}
func (f *runFixture) git(args ...string) string { f.t.Helper(); return gitAt(f.t, f.root, args...) }
func gitAt(t *testing.T, root string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = root
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}
func (f *runFixture) commit(message string) {
	f.t.Helper()
	f.git("add", "-A")
	f.git("commit", "-qm", message)
}
func (f *runFixture) setConfig(s string) { f.t.Helper(); f.write(f.config, s) }
func (f *runFixture) configure(s string) {
	f.t.Helper()
	b, _ := os.ReadFile(f.config)
	f.setConfig(string(b) + s)
}
func (f *runFixture) replace(path, old, new string) {
	f.t.Helper()
	f.write(path, strings.ReplaceAll(f.read(path), old, new))
	f.commit("edit")
}
func (f *runFixture) steps(steps ...string) {
	f.t.Helper()
	_ = os.Remove(f.scenario + ".n")
	_ = os.Remove(f.scenario + ".log")
	for i, s := range steps {
		f.write(filepath.Join(f.scenario, fmt.Sprintf("%d.sh", i)), s)
	}
}
func (f *runFixture) command(args ...string) *exec.Cmd {
	c := exec.Command(f.binary, args...)
	c.Dir = f.root
	return c
}
func (f *runFixture) cli(args ...string) runResult {
	f.t.Helper()
	c := f.command(args...)
	var out, errOut bytes.Buffer
	c.Stdout, c.Stderr = &out, &errOut
	err := c.Run()
	code := 0
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			code = e.ExitCode()
		} else {
			f.t.Fatal(err)
		}
	}
	return runResult{code, out.String(), errOut.String()}
}
func (f *runFixture) run(steps []string, args ...string) runResult {
	f.t.Helper()
	f.steps(steps...)
	return f.cli(append([]string{"run", "--config", f.config, "--no-wait"}, args...)...)
}
func (f *runFixture) calls() []fakeCall {
	f.t.Helper()
	b, _ := os.ReadFile(f.scenario + ".log")
	var calls []fakeCall
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var c fakeCall
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			f.t.Fatal(err)
		}
		calls = append(calls, c)
	}
	return calls
}
func (f *runFixture) check(r runResult, code int, texts ...string) {
	f.t.Helper()
	if r.code != code {
		f.t.Fatalf("exit %d, want %d\n%s%s", r.code, code, r.out, r.err)
	}
	for _, s := range texts {
		if !strings.Contains(r.out+r.err, s) {
			f.t.Errorf("missing %q:\n%s%s", s, r.out, r.err)
		}
	}
}
func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }
func emit(v any) string {
	b, _ := json.Marshal(v)
	return "printf '%s\\n' " + shellQuote(string(b)) + "\n"
}
func doneReply(fields ...map[string]any) string {
	r := map[string]any{"status": "done", "handoff": []string{"готово"}}
	for _, fields := range fields {
		for k, v := range fields {
			r[k] = v
		}
	}
	return envelope(r)
}
func envelope(result map[string]any) string {
	return emit(map[string]any{"type": "result", "is_error": false, "result": "ok", "session_id": "s1", "structured_output": result})
}
func writeReply(file string) string { return "printf a > " + shellQuote(file) + "\n" + doneReply() }
func quotaEvent(five, seven float64, reset int) string {
	return emit(map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{"unifiedWindows": map[string]any{"five_hour": map[string]any{"utilization": five, "resetsAt": reset}, "seven_day": map[string]any{"utilization": seven, "resetsAt": 1792476000}}}})
}

func TestRunCli(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		config string
		code   int
		text   string
	}{
		{"dry", []string{"--dry-run"}, "", 0, "agent: fake\nmodel: -\neffort: -"},
		{"prompt-arg", []string{"--dry-run", "--model", "m1"}, "[tiers]\ndefault = {agent = \"x\"}\n[agents.x]\ncmd = [\"x\", \"{prompt}\"]\n", 0, "stdin: none"},
		{"effort", []string{"--dry-run", "--effort", "low"}, "", 0, "effort: low"},
		{"unknown", []string{"--dry-run", "--agent", "nope"}, "", 2, "unknown agent 'nope'"},
		{"bad-config", []string{"--dry-run"}, "[agents.claude]\nmodle = 1\n", 2, "unknown key 'modle'"},
		{"attempts", []string{"--attempts", "0"}, "", 2, "--attempts must be at least 1"},
		{"replans", []string{"--replans", "-1"}, "", 2, "--replans must not be negative"},
		{"all-needs-dry", []string{"--all"}, "", 2, "--all needs --dry-run"},
		{"plan-needs-dry", []string{"--plan", "P001"}, "", 2, "--plan needs --dry-run"},
		{"zero", []string{"--max-units", "0"}, "", 1, "nothing to do"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRunFixture(t)
			if tc.config != "" {
				f.setConfig(tc.config)
			}
			before := f.read(planFile)
			r := f.run(nil, tc.args...)
			f.check(r, tc.code, tc.text)
			if f.read(planFile) != before || len(f.calls()) != 0 {
				t.Fatal("validation changed the plan or called the agent")
			}
		})
	}
	f := newRunFixture(t)
	f.root = t.TempDir()
	r := f.cli("run", "--print-config")
	f.check(r, 0, "[tiers]")
}

func TestRunLoopTwoUnits(t *testing.T) {
	f := newRunFixture(t)
	t.Setenv("DOCFLOW_RUNNER_UNIT", "P999.1.1")
	r := f.run([]string{"printf a > a.txt\n" + doneReply(map[string]any{"handoff": []string{"первое"}}), "printf b > b.txt\n" + doneReply(map[string]any{"decisions": []string{"д — п — а"}})})
	f.check(r, 0, "P001: ready to close", "run: 2 units,")
	if f.git("log", "--format=%s") != "P001.1.2: second unit\nP001.1.1: first unit\ninit" {
		t.Fatal(f.git("log", "--format=%s"))
	}
	for _, s := range []string{"Handoff:\n- первое\n", "- д — п — а"} {
		if !strings.Contains(f.read(planFile), s) {
			t.Error(s)
		}
	}
	if f.git("status", "--porcelain") != "" {
		t.Fatal("dirty tree")
	}
	calls := f.calls()
	if len(calls) != 2 || strings.Contains(strings.Join(calls[1].Argv, " "), "--resume") {
		t.Fatal(calls)
	}
	for _, s := range []string{"## Cycle (runner mode)", "Run `Verify` and tests in the foreground", "Do not end your reply while", "Цель {model}."} {
		if !strings.Contains(calls[0].Stdin, s) {
			t.Error(s)
		}
	}
	if strings.Contains(f.read("docs/plans/P002-other.md"), "[~]") {
		t.Fatal("crossed the plan boundary")
	}
}

func TestRunReusesAgentVerify(t *testing.T) {
	f := newRunFixture(t)
	f.replace(planFile, "status: active", "status: active\ngate: printf x >> .git/verify-count")
	step := "printf a > a.txt\n" + shellQuote(f.binary) + " verify P001.1.1 >/dev/null\n" + doneReply()
	r := f.run([]string{step}, "--max-units", "1")
	f.check(r, 0, "P001.1.1: verify reused (tree unchanged since the agent's verify)")
	count, err := os.ReadFile(filepath.Join(f.root, ".git", "verify-count"))
	if err != nil || string(count) != "x" {
		t.Fatalf("gate count = %q, %v; want one run", count, err)
	}
}

func TestRunIgnoresSkipRecord(t *testing.T) {
	f := newRunFixture(t)
	f.replace(planFile, "status: active", "status: active\ngate: printf x >> .git/verify-count")
	step := "printf a > a.txt\n" + shellQuote(f.binary) + " verify P001.1.1 --skip 'agent cannot verify' >/dev/null\n" + doneReply()
	r := f.run([]string{step}, "--max-units", "1")
	f.check(r, 0)
	if strings.Contains(r.out+r.err, "verify reused") {
		t.Fatal("runner reused a skipped verification")
	}
	count, err := os.ReadFile(filepath.Join(f.root, ".git", "verify-count"))
	if err != nil || string(count) != "x" {
		t.Fatalf("gate count = %q, %v; want runner's full verify", count, err)
	}
}

func TestRunLoopRetries(t *testing.T) {
	for _, mode := range []string{"verify", "preflight", "gate", "hook", "no-session", "invalid-unverified"} {
		t.Run(mode, func(t *testing.T) {
			f := newRunFixture(t)
			steps := []string{doneReply(), writeReply("a.txt")}
			needle := "docflow verify failed"
			switch mode {
			case "preflight":
				f.replace("docs/docflow.md", "read_budget: 40000", "read_budget: 40000\npreflight: test -f lint.ok")
				steps = []string{writeReply("a.txt"), "touch lint.ok\n" + doneReply()}
				needle = "preflight failed"
			case "gate":
				f.replace(planFile, "status: active", "status: active\ngate: test -f gate.ok")
				steps = []string{writeReply("a.txt"), "touch gate.ok\n" + doneReply()}
				needle = "gate failed"
			case "hook":
				f.write(".git/hooks/pre-commit", "#!/bin/sh\ntest -f lint.ok || { echo fix-lint; exit 1; }\n")
				_ = os.Chmod(filepath.Join(f.root, ".git/hooks/pre-commit"), 0o755)
				steps = []string{writeReply("a.txt"), "touch lint.ok\n" + doneReply()}
				needle = "fix-lint"
			case "no-session":
				steps = []string{"echo no-json\nexit 3\n", writeReply("a.txt")}
				needle = "## Previous attempt"
			case "invalid-unverified":
				steps = []string{doneReply(map[string]any{"unverified": "всё"}), writeReply("a.txt")}
				needle = "unverified must be a list of strings"
			}
			r := f.run(steps, "--max-units", "1")
			f.check(r, 0)
			calls := f.calls()
			if len(calls) != 2 || !strings.Contains(calls[1].Stdin, needle) {
				t.Fatal(calls)
			}
			if mode == "verify" && !strings.Contains(calls[1].Stdin, "log: .git/docflow/run/P001.1.1.verify.log") {
				t.Fatalf("verify retry omitted the complete log path: %s", calls[1].Stdin)
			}
			if mode != "no-session" && strings.Join(calls[1].Argv, " ") != "--resume s1" {
				t.Fatal(calls)
			}
			if strings.Count(f.read(planFile), "- готово\n") != 1 {
				t.Fatal("duplicated handoff")
			}
			if mode == "hook" && !strings.Contains(calls[1].Stdin, "do not disable hooks, signing or checks") {
				t.Fatal(calls[1])
			}
		})
	}
}

func TestRunLogNamesUnique(t *testing.T) {
	f := newRunFixture(t)
	first := f.run([]string{"exit 1"}, "--attempts", "1", "--max-units", "1")
	if first.code == 0 {
		t.Fatalf("first run unexpectedly succeeded:\n%s%s", first.out, first.err)
	}
	second := f.run([]string{"echo a > a.txt\n" + doneReply()}, "--max-units", "1")
	f.check(second, 0, "P001.1.1 done:")

	logs := filepath.Join(f.root, ".git", "docflow", "run")
	firstLog := filepath.Join(logs, "P001.1.1.1.out")
	secondLog := filepath.Join(logs, "P001.1.1.2.out")
	firstData, err := os.ReadFile(firstLog)
	if err != nil {
		t.Fatal(err)
	}
	secondData, err := os.ReadFile(secondLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(firstData), "--- exit 1") || !strings.Contains(string(secondData), "--- exit 0") {
		t.Fatalf("call logs do not preserve both runs:\nfirst: %s\nsecond: %s", firstData, secondData)
	}
}

func TestZeroTimeoutNeverKills(t *testing.T) {
	for _, timeout := range []int{0, -1} {
		t.Run(fmt.Sprintf("%d", timeout), func(t *testing.T) {
			f := newRunFixture(t)
			f.configure(fmt.Sprintf("timeout = %d\n", timeout))
			r := f.run([]string{"sleep 0.1\n" + writeReply("a.txt")}, "--attempts", "1", "--max-units", "1")
			f.check(r, 0, "run: 1 unit,")
			if len(f.calls()) != 1 || !strings.Contains(f.read(planFile), "[x] P001.1.1") {
				t.Fatalf("calls=%v\n%s", f.calls(), f.read(planFile))
			}
		})
	}
}

func TestRunPlanTimeout(t *testing.T) {
	profile := map[string]any{"timeout": 1}
	plan := &format.Plan{Doc: format.Doc{Meta: map[string]string{"timeout": "60"}}}
	if got := agentCallTimeout(plan, profile); got != 60*time.Second {
		t.Fatalf("plan timeout = %s, want 1m", got)
	}
	if got := agentCallTimeout(&format.Plan{Doc: format.Doc{Meta: map[string]string{}}}, profile); got != time.Second {
		t.Fatalf("profile timeout = %s, want 1s", got)
	}

	f := newRunFixture(t)
	f.configure("timeout = 1\n")
	f.replace(planFile, "created: 2026-10-12\n", "created: 2026-10-12\ntimeout: 60\n")
	r := f.run([]string{"sleep 1.1\n" + writeReply("a.txt")}, "--attempts", "1", "--max-units", "1")
	f.check(r, 0, "P001.1.1 done:")
	if len(f.calls()) != 1 {
		t.Fatalf("calls = %d, want one completed agent call", len(f.calls()))
	}
}

func TestRunLoopFailures(t *testing.T) {
	for _, tc := range []struct {
		name, step, config, text string
		attempts, code           int
	}{
		{"verify-exhausted", doneReply(), "", "gave up after 2 attempts", 2, 4},
		{"no-result", "echo just-text\n", "", "no result JSON", 1, 7},
		{"blocked", envelope(map[string]any{"status": "blocked", "reason": "нет файла"}), "", "blocked (needs human): нет файла", 1, 3},
		{"plan-block", envelope(map[string]any{"status": "blocked", "blocker": "plan", "reason": "Do расходится с кодом"}), "", "blocked (needs plan): Do расходится с кодом", 1, 3},
		{"timeout", "sleep 5\n" + doneReply(), "timeout = 1\n", "timed out after 1 s", 1, 7},
		{"limit", emit(map[string]any{"is_error": true, "result": "hit your limit", "session_id": "s1"}), "error = \"json:is_error\"\nlimit = \"hit your limit\"\n", "usage limit (--no-wait)", 3, 7},
		{"auth", emit(map[string]any{"is_error": true, "result": "Failed to authenticate: OAuth expired"}) + "exit 1\n", "error = \"json:is_error\"\nauth = \"failed to authenticate\"\n", "fake is not logged in", 3, 7},
		{"error-reason", emit(map[string]any{"is_error": true, "result": "boom\nmore"}), "error = \"json:is_error\"\n", "the agent reported an error: boom", 1, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRunFixture(t)
			f.configure(tc.config)
			steps := []string{}
			for i := 0; i < tc.attempts; i++ {
				steps = append(steps, tc.step)
			}
			r := f.run(steps, "--attempts", fmt.Sprint(tc.attempts))
			f.check(r, tc.code, tc.text, "run: 0 units,")
			if tc.name == "auth" || tc.name == "limit" {
				if len(f.calls()) != 1 {
					t.Fatal(f.calls())
				}
			}
			if tc.code == 3 && !strings.Contains(f.read(planFile), "[!] P001.1.1") {
				t.Fatal(f.read(planFile))
			}
		})
	}
}

func TestRunPendingCommit(t *testing.T) {
	f := newRunFixture(t)
	f.write(".git/hooks/pre-commit", "#!/bin/sh\necho fix-lint; exit 1\n")
	_ = os.Chmod(filepath.Join(f.root, ".git/hooks/pre-commit"), 0o755)
	r := f.run([]string{writeReply("a.txt")}, "--attempts", "1", "--max-units", "1")
	f.check(r, 4, "has a pending commit")
	f.check(f.cli("status"), 0, "commit pending", "current: P001.1.1")
	_ = os.Remove(filepath.Join(f.root, ".git/hooks/pre-commit"))
	r = f.run(nil, "--max-units", "1")
	f.check(r, 0, "recovered commit")
	if strings.Count(r.out, "P001.1.1 time: ") != 1 {
		t.Fatalf("recovered commit should report elapsed time once:\n%s", r.out)
	}
	if len(f.calls()) != 0 || strings.Count(f.git("log", "--format=%s"), "P001.1.1:") != 1 {
		t.Fatal(f.calls())
	}
}

func TestRunResumesAfterMigrate(t *testing.T) {
	f := newRunFixture(t)
	f.write("docs/docflow.md", strings.Replace(f.read("docs/docflow.md"), "docflow: 3", "docflow: 1", 1))
	f.write(planFile, strings.Replace(f.read(planFile), "### [ ] P001.1.1", "### [~] P001.1.1", 1))
	f.commit("legacy working unit")
	f.steps(writeReply("a.txt"))

	before := f.run(nil, "--max-units", "1")
	f.check(before, 2, "docflow migrate")
	if len(f.calls()) != 0 {
		t.Fatalf("agent called before migrate: %+v", f.calls())
	}

	f.check(f.cli("migrate"), 0)
	if !strings.Contains(f.read("docs/docflow.md"), "docflow: 3") {
		t.Fatal("migrate did not update docs/docflow.md")
	}
	after := f.run(nil, "--max-units", "1")
	f.check(after, 0, "run: 1 unit,")
	if len(f.calls()) != 1 || !strings.Contains(f.read(planFile), "[x] P001.1.1") || !strings.Contains(f.git("log", "--format=%s"), "P001.1.1: first unit") {
		t.Fatalf("calls=%+v\n%s\n%s", f.calls(), f.read(planFile), f.git("log", "--format=%s"))
	}
}

func TestRunOrderAndGitFailures(t *testing.T) {
	for _, mode := range []string{"agent-commit", "foreign-commit", "plan-commit", "amend", "status", "signing", "guard", "dirty", "stale-read"} {
		t.Run(mode, func(t *testing.T) {
			f := newRunFixture(t)
			step := writeReply("a.txt")
			code, text := 5, ""
			switch mode {
			case "agent-commit":
				step = "echo a > a.txt\ngit add -A\ngit commit -qm agent\n" + doneReply()
				text = "agent committed"
			case "foreign-commit":
				step = "echo a > a.txt\ngit add a.txt\ngit commit -qm foreign\n" + doneReply()
				code, text = 0, "HEAD moved by 1 commit"
			case "plan-commit":
				step = "git add " + planFile + "\ngit commit -qm agent\n" + doneReply()
				text = "the runner commits units itself"
			case "amend":
				step = "git commit --amend -qm rewritten\n" + doneReply()
				text = "is no longer an ancestor of HEAD"
			case "status":
				step = `sed 's/\[~\] P001.1.1/[x] P001.1.1/' ` + planFile + " > p.tmp\nmv p.tmp " + planFile + "\n" + doneReply()
				text = "the agent changed the unit to [x]"
			case "signing":
				f.git("config", "commit.gpgsign", "true")
				f.git("config", "gpg.program", "/usr/bin/false")
				code, text = 2, "git commit failed"
			case "guard":
				step = "echo a > a.txt\n"
				for _, cmd := range []string{"finish", "done", "commit", "block"} {
					step += shellQuote(f.binary) + " " + cmd + " P001.1.1"
					switch cmd {
					case "finish", "done":
						step += " --handoff=x"
					case "block":
						step += " --reason=x"
					}
					step += " >guard.out 2>&1 && exit 90\ntest \"$?\" = 5 || exit 91\n"
				}
				step += "grep 'the runner drives P001.1.1' guard.out >/dev/null\n" + doneReply()
				code, text = 0, "done:"
			case "dirty":
				f.write("junk.txt", "x")
				text = "working tree is dirty"
			case "stale-read":
				f.write("c.txt", "c")
				f.replace(planFile, "- docs/docflow.md\nDo:\n- write b", "- c.txt\nDo:\n- write b")
				step = "rm c.txt\n" + writeReply("a.txt")
				code, text = 2, "P001.1.2: the plan no longer matches the tree"
			}
			args := []string{"--max-units", "1"}
			if mode == "stale-read" {
				args = nil
			}
			r := f.run([]string{step}, args...)
			f.check(r, code, text)
			if mode == "signing" && len(f.calls()) != 1 {
				t.Fatal(f.calls())
			}
			if mode == "dirty" && len(f.calls()) != 0 {
				t.Fatal(f.calls())
			}
		})
	}
}

func TestRunStartPrerequisitesAndPermissions(t *testing.T) {
	for _, mode := range []string{"full-refused", "full-yes", "manual-refused", "manual-yes", "prerequisite-failed", "dry"} {
		t.Run(mode, func(t *testing.T) {
			f := newRunFixture(t)
			args := []string{"--max-units", "1"}
			code, text := 2, "--yes"
			if strings.HasPrefix(mode, "full") || mode == "dry" {
				f.replace(planFile, "status: active", "status: active\npermissions: full")
				f.configure("[agents.fake.permissions]\nfull = [\"--no-sandbox\"]\n")
			}
			if strings.HasPrefix(mode, "manual") || mode == "dry" {
				f.replace(planFile, "## Traps", "## Prerequisites\n- creds: `test -f docs/docflow.md` (once)\n- вход в симулятор\n\n## Traps")
			}
			if strings.HasSuffix(mode, "yes") {
				args = append(args, "--yes")
				code, text = 0, "done:"
			}
			if mode == "prerequisite-failed" {
				f.replace(planFile, "## Traps", "## Prerequisites\n- token: `echo no-token >&2; exit 3`\n- input: `read x`\n\n## Traps")
				f.configure("stream = \"claude\"\nquota = [\"touch\", " + jsonString(filepath.Join(f.tmp, "probed")) + "]\n")
				text = "prerequisite failed: token (echo no-token >&2; exit 3, exit 3): no-token"
			}
			if mode == "dry" {
				args = []string{"--dry-run"}
				code, text = 0, "prerequisites:"
			}
			r := f.run([]string{writeReply("a.txt")}, args...)
			f.check(r, code, text)
			if mode == "full-yes" && !strings.Contains(strings.Join(f.calls()[0].Argv, " "), "--no-sandbox") {
				t.Fatal(f.calls())
			}
			if mode == "manual-yes" && !strings.Contains(f.calls()[0].Stdin, "вход в симулятор") {
				t.Fatal(f.calls())
			}
			if code == 2 || mode == "dry" {
				if len(f.calls()) != 0 {
					t.Fatal(f.calls())
				}
			}
			if mode == "dry" && !strings.Contains(r.out, "creds: `test -f docs/docflow.md` (once)") {
				t.Fatalf("dry-run omitted once marker:\n%s", r.out)
			}
			if _, err := os.Stat(filepath.Join(f.tmp, "probed")); err == nil {
				t.Fatal("quota ran before prerequisites")
			}
		})
	}
}

func TestRunPrerequisiteOnce(t *testing.T) {
	t.Run("started plan skips failing command and confirmation", func(t *testing.T) {
		f := newRunFixture(t)
		f.replace(planFile, "### [ ] P001.1.1", "### [x] P001.1.1")
		f.replace(planFile, "## Traps", "## Prerequisites\n- code baseline: `echo no-baseline >&2; exit 3` (once)\n- code review baseline (once)\n\n## Traps")
		r := f.run([]string{writeReply("b.txt")}, "--max-units", "1")
		f.check(r, 0, "prerequisite skipped (once, plan started): code baseline", "prerequisite skipped (once, plan started): code review baseline", "run: 1 unit,")
		if len(f.calls()) != 1 {
			t.Fatalf("agent calls = %d, want 1", len(f.calls()))
		}
	})

	t.Run("not-started plan fails the once command", func(t *testing.T) {
		f := newRunFixture(t)
		f.replace(planFile, "## Traps", "## Prerequisites\n- code baseline: `echo no-baseline >&2; exit 3` (once)\n\n## Traps")
		r := f.run(nil, "--max-units", "1")
		f.check(r, 2, "prerequisite failed: code baseline (echo no-baseline >&2; exit 3, exit 3): no-baseline")
		if len(f.calls()) != 0 {
			t.Fatalf("agent called despite failed prerequisite: %+v", f.calls())
		}
	})
}

// Acceptance for P033: one scenario per claim of the plan Goal.
func TestRunOncePrerequisitesAcceptance(t *testing.T) {
	const failing = "- baseline: `echo no-baseline >&2; exit 3` (once)\n"
	const passing = "- env: `test -f docs/docflow.md`\n"
	prereqs := func(f *runFixture, items string) {
		f.replace(planFile, "## Traps", "## Prerequisites\n"+items+"\n## Traps")
	}
	start := func(f *runFixture) { f.replace(planFile, "### [ ] P001.1.1", "### [x] P001.1.1") }

	t.Run("started plan skips a failing once item", func(t *testing.T) {
		f := newRunFixture(t)
		start(f)
		prereqs(f, failing+passing)
		r := f.run([]string{writeReply("b.txt")}, "--max-units", "1")
		f.check(r, 0, "prerequisite skipped (once, plan started): baseline", "run: 1 unit,")
		if len(f.calls()) != 1 {
			t.Fatalf("agent calls = %d, want 1", len(f.calls()))
		}
	})

	t.Run("not-started plan checks the once item", func(t *testing.T) {
		f := newRunFixture(t)
		prereqs(f, failing+passing)
		r := f.run([]string{writeReply("a.txt")}, "--max-units", "1")
		f.check(r, 2, "prerequisite failed: baseline")
		if len(f.calls()) != 0 {
			t.Fatalf("agent called despite failed prerequisite: %+v", f.calls())
		}
	})

	t.Run("started plan still checks an unmarked item", func(t *testing.T) {
		f := newRunFixture(t)
		start(f)
		prereqs(f, "- token: `echo no-token >&2; exit 3`\n")
		r := f.run([]string{writeReply("b.txt")}, "--max-units", "1")
		f.check(r, 2, "prerequisite failed: token")
		if len(f.calls()) != 0 {
			t.Fatalf("agent called despite failed prerequisite: %+v", f.calls())
		}
	})

	t.Run("started plan skips a once item without command and its confirmation", func(t *testing.T) {
		f := newRunFixture(t)
		start(f)
		prereqs(f, "- вход в симулятор (once)\n")
		r := f.run([]string{writeReply("b.txt")}, "--max-units", "1")
		f.check(r, 0, "prerequisite skipped (once, plan started): вход в симулятор", "run: 1 unit,")
		if strings.Contains(r.out+r.err, "--yes") || len(f.calls()) != 1 {
			t.Fatalf("confirmation asked or agent not called (calls=%d):\n%s%s", len(f.calls()), r.out, r.err)
		}
	})

	t.Run("dry-run all prints the marker and lint accepts both forms", func(t *testing.T) {
		f := newRunFixture(t)
		prereqs(f, failing+"- вход в симулятор (once)\n")
		r := f.cli("run", "--config", f.config, "--dry-run", "--all")
		f.check(r, 0, "baseline: `echo no-baseline >&2; exit 3` (once)", "вход в симулятор (once)")
		if len(f.calls()) != 0 {
			t.Fatalf("dry-run called the agent: %+v", f.calls())
		}
		lint := f.cli("lint")
		f.check(lint, 0)
		if strings.Contains(lint.out+lint.err, "error") {
			t.Fatalf("lint reported errors:\n%s%s", lint.out, lint.err)
		}
	})
}

func TestRunStopsAndLock(t *testing.T) {
	for _, mode := range []string{"max-units", "soft-stop", "stale-stop", "lock", "stop-command", "empty", "blocked"} {
		t.Run(mode, func(t *testing.T) {
			f := newRunFixture(t)
			args := []string{}
			steps := []string{writeReply("a.txt"), writeReply("b.txt")}
			code, text := 0, ""
			switch mode {
			case "max-units":
				args = []string{"--max-units", "1"}
				text = "run: 1 unit,"
			case "soft-stop":
				steps[0] = "touch .git/docflow/run/stop\n" + steps[0]
				text = "run: stopped on request"
			case "stale-stop":
				f.write(".git/docflow/run/stop", "")
				text = "ready to close"
			case "lock":
				f.write(".git/docflow/run/run.lock", fmt.Sprint(os.Getpid()))
				code, text = 2, "another runner is active"
			case "stop-command":
				r := f.cli("run", "--stop")
				f.check(r, 1, "no active runner")
				f.write(".git/docflow/run/run.lock", fmt.Sprint(os.Getpid()))
				f.check(f.cli("run", "--stop"), 0, "stops after its current unit")
				return
			case "empty":
				f.replace("docs/docflow.md", "- P001\n- P002\n", "")
				code, text = 1, "nothing to do"
			case "blocked":
				f.replace(planFile, "[ ] P001.1.1", "[!] P001.1.1")
				code, text = 3, "P001.1.1 is blocked"
			}
			r := f.run(steps, args...)
			f.check(r, code, text)
			if mode == "soft-stop" || mode == "max-units" {
				if len(f.calls()) != 1 {
					t.Fatal(f.calls())
				}
			}
			if mode != "lock" {
				if _, err := os.Stat(filepath.Join(f.root, ".git/docflow/run/run.lock")); err == nil {
					t.Fatal("lock left behind")
				}
			}
		})
	}
}

func TestRunSubdirectoryAndNested(t *testing.T) {
	for _, mode := range []string{"subdir", "nested-commit", "nested-hook", "nested-amend"} {
		t.Run(mode, func(t *testing.T) {
			f := newRunFixture(t)
			steps := []string{writeReply("a.txt")}
			code, text := 0, "done:"
			if mode == "subdir" {
				sub := filepath.Join(f.root, "sub")
				_ = os.Mkdir(sub, 0o755)
				if err := os.Rename(filepath.Join(f.root, "docs"), filepath.Join(sub, "docs")); err != nil {
					t.Fatal(err)
				}
				f.commit("move")
				f.root = sub
				steps = append(steps, writeReply("b.txt"))
				text = "ready to close"
			} else {
				f.replace("docs/docflow.md", "read_budget: 40000", "read_budget: 40000\nrepos: projects/*")
				f.write(".gitignore", "/projects/\n")
				f.commit("repos")
				for _, n := range []string{"a", "b"} {
					f.write("projects/"+n+"/main.swift", "let a = 1\n")
					dir := filepath.Join(f.root, "projects", n)
					for _, a := range [][]string{{"init", "-q"}, {"config", "user.name", "t"}, {"config", "user.email", "t@example.invalid"}, {"config", "commit.gpgsign", "false"}, {"add", "-A"}, {"commit", "-qm", "init"}} {
						gitAt(t, dir, a...)
					}
				}
				switch mode {
				case "nested-commit":
					steps[0] = "echo a > projects/a/main.swift\ngit -C projects/a add -A\ngit -C projects/a commit -qm agent\n" + steps[0]
					text = "HEAD moved by 1 commit(s): projects/a:"
				case "nested-amend":
					steps[0] = "git -C projects/a commit --amend -qm rewrite\n" + steps[0]
					code, text = 5, "the agent rewrote history in projects/a"
				case "nested-hook":
					f.write("projects/b/.git/hooks/pre-commit", "#!/bin/sh\ntest -f lint.ok || { echo fix-lint; exit 1; }\n")
					_ = os.Chmod(filepath.Join(f.root, "projects/b/.git/hooks/pre-commit"), 0o755)
					steps = []string{"echo a > projects/a/main.swift\necho b > projects/b/main.swift\n" + steps[0], "touch projects/b/lint.ok\n" + doneReply()}
				}
			}
			args := []string{"--max-units", "1"}
			if mode == "subdir" {
				args = nil
			}
			r := f.run(steps, args...)
			f.check(r, code, text)
			if mode == "nested-hook" {
				if strings.Contains(r.out, "HEAD moved") || len(f.calls()) != 2 {
					t.Fatal(r.out, r.err)
				}
				for _, n := range []string{"a", "b"} {
					if !strings.Contains(f.git("log", "-1", "--format=%B"), "Repo: projects/"+n+" ") {
						t.Fatal("missing repo receipt")
					}
				}
			}
		})
	}
}

func TestRunProgressAndCost(t *testing.T) {
	for _, mode := range []string{"default", "growth", "rollover", "verbose", "blocked", "gave-up", "unverified", "cost"} {
		t.Run(mode, func(t *testing.T) {
			f := newRunFixture(t)
			f.configure("stream = \"claude\"\n")
			steps := []string{quotaEvent(.08, .07, 1791904200) + writeReply("a.txt"), quotaEvent(.11, .08, 1791904200) + writeReply("b.txt")}
			args := []string{}
			code, text := 0, "5h 11% (+3) · 7d 8% (+1)"
			switch mode {
			case "default":
				steps = []string{writeReply("a.txt"), writeReply("b.txt")}
				text = "run: 2 units,"
			case "rollover":
				steps[1] = quotaEvent(.02, .08, 1791922200) + writeReply("b.txt")
				text = "5h 2% · 7d 8% (+1)"
			case "verbose":
				args = []string{"--verbose"}
				text = "  attempt 1, log .git/docflow/run/P001.1.1.1.out"
			case "blocked":
				steps = []string{quotaEvent(.08, .07, 1791904200) + envelope(map[string]any{"status": "blocked", "reason": "nope"})}
				code, text = 3, "run: 0 units,"
			case "gave-up":
				steps = []string{quotaEvent(.08, .07, 1791904200) + doneReply()}
				args = []string{"--attempts", "1"}
				code, text = 4, "run: 0 units,"
			case "unverified":
				steps[0] = "echo a > a.txt\n" + doneReply(map[string]any{"unverified": []string{"раскладка\n не видна"}})
				text = ", 1 unverified"
			case "cost":
				f.configure("cost = \"json:total_cost_usd\"\n")
				makeStep := func(cost float64) string {
					return emit(map[string]any{"type": "result", "session_id": "s1", "total_cost_usd": cost, "structured_output": map[string]any{"status": "done", "handoff": []string{"h"}}})
				}
				steps = []string{makeStep(.25), "echo a > a.txt\n" + makeStep(.5)}
				args = []string{"--max-units", "1"}
				text = "$0.75"
			}
			r := f.run(steps, args...)
			f.check(r, code, text)
			if strings.Contains(r.out, "\x1b") {
				t.Fatal("ANSI in piped output")
			}
			wantTimes := 2
			if mode == "blocked" || mode == "gave-up" || mode == "cost" {
				wantTimes = 1
			}
			if got := strings.Count(r.out, " time: "); got != wantTimes {
				t.Fatalf("duration rows %d != %d:\n%s", got, wantTimes, r.out)
			}
			if mode == "verbose" {
				f.check(r, 0, "  eta ~", "  P001.1.1,", "resets ")
			}
			if mode == "default" && (strings.Contains(r.out, "quota:") || strings.Contains(r.out, "attempt 1, log")) {
				t.Fatal(r.out)
			}
			if mode == "unverified" && !strings.Contains(f.read(planFile), "- Unverified: раскладка не видна") {
				t.Fatal(f.read(planFile))
			}
			if mode == "gave-up" || mode == "blocked" {
				lines := strings.Split(strings.TrimSpace(r.out), "\n")
				if lines[len(lines)-2] != "quota: fake 5h 8% · 7d 7%" {
					t.Fatal(r.out)
				}
			}
		})
	}
}

func TestRunNotify(t *testing.T) {
	for _, mode := range []string{"ready", "blocked", "soft-stop", "prerequisite", "flag-off", "env-off"} {
		t.Run(mode, func(t *testing.T) {
			f := newRunFixture(t)
			stub := filepath.Join(f.tmp, "bin")
			log := filepath.Join(f.tmp, "notify.log")
			for _, name := range []string{"osascript", "notify-send"} {
				path := filepath.Join(stub, name)
				f.write(path, "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$STUB_LOG\"\n")
				_ = os.Chmod(path, 0o755)
			}
			t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("STUB_LOG", log)
			t.Setenv("DOCFLOW_NOTIFY", "1")
			steps := []string{writeReply("a.txt"), writeReply("b.txt")}
			code, text := 0, "P001: ready to close"
			args := []string{}
			if mode == "blocked" || strings.HasSuffix(mode, "off") {
				steps = []string{envelope(map[string]any{"status": "blocked", "reason": "нет файла"})}
				code, text = 3, "P001.1.1: blocked (needs human): нет файла"
			}
			if mode == "soft-stop" {
				steps[0] = "touch .git/docflow/run/stop\n" + steps[0]
				text = "stopped on request"
			}
			if mode == "prerequisite" {
				f.replace(planFile, "## Traps", "## Prerequisites\n- server: `false`\n\n## Traps")
				code, text = 2, "a prerequisite failed"
			}
			if mode == "flag-off" {
				args = append(args, "--no-notify")
			}
			if mode == "env-off" {
				t.Setenv("DOCFLOW_NOTIFY", "0")
			}
			r := f.run(steps, args...)
			f.check(r, code)
			b, err := os.ReadFile(log)
			if strings.HasSuffix(mode, "off") {
				if err == nil {
					t.Fatal(string(b))
				}
			} else if err != nil || !strings.Contains(string(b), text) {
				t.Fatalf("notification %q: %v, want %q", b, err, text)
			}
		})
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func TestRunLimitsResumeWithoutSpendingAttempt(t *testing.T) {
	f := newRunFixture(t)
	f.configure("error = \"json:is_error\"\nlimit = \"hit your limit\"\nlimit_wait = 0\n")
	f.steps(emit(map[string]any{"is_error": true, "result": "hit your limit · resets 1700000000", "session_id": "s1"}), writeReply("a.txt"))
	r := f.cli("run", "--config", f.config, "--attempts", "1", "--max-units", "1")
	f.check(r, 0, "usage limit until", "(1 attempt,")
	calls := f.calls()
	if len(calls) != 2 || strings.Join(calls[1].Argv, " ") != "--resume s1" || !strings.Contains(calls[1].Stdin, "stopped on a provider refusal") {
		t.Fatal(calls)
	}
}

func TestRunSuccessfulQuotedErrorsAndResultFallback(t *testing.T) {
	for _, mode := range []string{"limit", "auth", "fenced", "bare"} {
		t.Run(mode, func(t *testing.T) {
			f := newRunFixture(t)
			f.configure("error = \"json:is_error\"\nlimit = \"rate limit\"\nauth = \"not logged in\"\n")
			step := "echo a > a.txt\n"
			switch mode {
			case "limit", "auth":
				step += emit(map[string]any{"is_error": false, "result": "quoted: rate limit; not logged in", "structured_output": map[string]any{"status": "done", "handoff": []string{"h"}}})
			case "fenced":
				step += "printf '%s\\n' " + shellQuote("Сделано.\n```json\n{\"status\":\"done\",\"handoff\":[\"h\"]}\n```\n") + "\n"
			case "bare":
				step += "printf '%s\\n' " + shellQuote(`{"status":"done","handoff":["h"]}`) + "\n"
			}
			f.check(f.run([]string{step}, "--max-units", "1"), 0, "done:")
		})
	}
}

func TestRunCodexFailedTurnResumesAndRecordsTokens(t *testing.T) {
	f := mixedRunFixture(t, false)
	f.configure("tokens = [\"json:usage.input_tokens\"]\noutput_tokens = \"json:usage.output_tokens\"\n")
	failed := emit(map[string]any{"type": "thread.started", "thread_id": "t1"}) + emit(map[string]any{"type": "turn.failed", "error": map[string]any{"message": "boom"}})
	done := codexStep(f, 1, 20, 30, true)
	// Force the same codex route for the first legacy unit.
	r := f.run([]string{failed, done}, "--agent", "cx", "--max-units", "1", "--verbose")
	f.check(r, 0, "attempt 1: the agent reported an error", "10 in / 1 out")
	calls := f.calls()
	if len(calls) != 2 || !strings.HasSuffix(strings.Join(calls[1].Argv, " "), "resume t1") {
		t.Fatal(calls)
	}
	b, err := os.ReadFile(filepath.Join(f.root, ".git/docflow/stats/P001.1.1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stats map[string]any
	if err := json.Unmarshal(b, &stats); err != nil {
		t.Fatal(err)
	}
	if stats["tokens"] != float64(10) || stats["calls"] != float64(2) {
		t.Fatal(stats)
	}
}
