package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/agents"
)

// The acceptance tests drive the built bench CLI end to end. The run and
// interrupt scenarios work on a temporary copy of the checkout layout
// (go.mod, bench/, tools/bench/testdata) under git, with the built docflow
// CLI and fake codex and pi agents in PATH. POSIX only: the builtin agent
// profiles are started as bare executable names, which Windows PATH
// resolution cannot satisfy with shell scripts. The validate and fairness
// scenarios use only Go and Git and run on every platform.

// acceptanceRoster sends every role to the fake pi agent, which reports a
// cost, and keeps the fake codex available for the runner's quota probe.
const acceptanceRoster = `spend = ["pi", "codex"]

[provider.pi]
check = false

[role]
cheap = "cheap"
executor = "standard"
heavy = "strong"
review = "strong"
acceptance = "trusted"
replan = "trusted"

[route.pi-cheap]
run = "pi | test-model | high"
tier = "cheap"

[route.pi-standard]
run = "pi | test-model | high"
tier = "standard"

[route.pi-strong]
run = "pi | test-model | high"
tier = "strong"

[route.codex-strong]
run = "codex | gpt-6.1-sol | high"
tier = "strong"
`

const acceptancePi = `#!/bin/sh
case "$*" in
  *'auth print-api-key'*) exit 1 ;;
esac
cat >/dev/null
printf 'package tinybase\n\nfunc Value() int { return 2 }\n' > value.go
printf '%s\n' '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"{\"status\":\"done\",\"handoff\":[\"tiny complete\"],\"decisions\":[],\"unverified\":[],\"reason\":\"\",\"blocker\":\"\"}"}],"usage":{"input":3,"cost":{"total":0.25}}}}'
`

const acceptanceCodex = `#!/bin/sh
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r request; do
    case "$request" in
      *'"id":1'*) printf '%s\n' '{"id":1,"result":{}}' ;;
      *'"id":2'*) printf '%s\n' '{"id":2,"result":{"rateLimits":[]}}'; exit 0 ;;
    esac
  done
  exit 0
fi
cat >/dev/null
printf 'package tinybase\n\nfunc Value() int { return 2 }\n' > value.go
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"done\",\"handoff\":[\"done\"],\"decisions\":[],\"unverified\":[],\"reason\":\"\",\"blocker\":\"\"}"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":3,"output_tokens":4}}'
`

type acceptanceEnv struct {
	t        *testing.T
	root     string // temporary checkout copy
	binDir   string // fake codex and pi
	benchBin string // built bench CLI
	docflow  string // built docflow CLI
	results  string // root/bench/results
}

func newAcceptanceEnv(t *testing.T) *acceptanceEnv {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake codex and pi agents are POSIX shell scripts; bare-name PATH lookup does not resolve them on Windows")
	}
	fixture, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	// Checkout layout: go.mod, bench/, tools/bench/testdata.
	goMod, err := os.ReadFile(filepath.Join(fixture, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	writeAcceptanceFile(t, root, "go.mod", string(goMod), 0o644)
	if err := copyTree(filepath.Join(fixture, "tools", "bench", "testdata"), filepath.Join(root, "tools", "bench", "testdata")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(filepath.Join(fixture, "tools", "bench", "testdata", "base"), filepath.Join(root, "bench", "base")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(filepath.Join(fixture, "tools", "bench", "testdata", "tasks"), filepath.Join(root, "bench", "tasks")); err != nil {
		t.Fatal(err)
	}
	fence, err := os.ReadFile(filepath.Join(fixture, "bench", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	writeAcceptanceFile(t, root, filepath.Join("bench", "go.mod"), string(fence), 0o644)
	writeAcceptanceFile(t, root, filepath.Join("bench", "roster.toml"), acceptanceRoster, 0o644)
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitBench(t, root, "init")
	gitBench(t, root, "config", "user.name", "bench acceptance")
	gitBench(t, root, "config", "user.email", "bench@localhost")
	gitBench(t, root, "add", "-A")
	gitBench(t, root, "commit", "-m", "base")

	bins := t.TempDir()
	e := &acceptanceEnv{
		t:        t,
		root:     root,
		binDir:   filepath.Join(bins, "fake-bin"),
		benchBin: nativeBin(bins, "bench"),
		docflow:  nativeBin(bins, "docflow"),
		results:  filepath.Join(root, "bench", "results"),
	}
	for target, pkg := range map[string]string{e.benchBin: "./tools/bench", e.docflow: "./cmd/docflow"} {
		build := exec.Command("go", "build", "-o", target, pkg)
		build.Dir = fixture
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", pkg, err, output)
		}
	}
	if err := os.MkdirAll(e.binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAcceptanceFile(t, e.binDir, "pi", acceptancePi, 0o755)
	writeAcceptanceFile(t, e.binDir, "codex", acceptanceCodex, 0o755)
	return e
}

func writeAcceptanceFile(t *testing.T, dir, name, text string, perm os.FileMode) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), perm); err != nil {
		t.Fatal(err)
	}
}

// command starts the built bench CLI in the checkout copy with the fake
// agents in PATH and an isolated configuration.
func (e *acceptanceEnv) command(args ...string) *exec.Cmd {
	c := exec.Command(e.benchBin, args...)
	c.Dir = e.root
	c.Env = append(os.Environ(),
		"PATH="+e.binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"XDG_CONFIG_HOME="+filepath.Join(e.root, "config"),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"DOCFLOW_NOTIFY=0",
		"DOCFLOW_SKILLS_CHECK=0",
	)
	return c
}

func (e *acceptanceEnv) run(args ...string) (string, string, int) {
	e.t.Helper()
	var out, errOut bytes.Buffer
	c := e.command(args...)
	c.Stdout, c.Stderr = &out, &errOut
	err := c.Run()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			e.t.Fatal(err)
		}
	}
	return out.String(), errOut.String(), code
}

// tinyRunArgs assembles the flags every tiny-task run shares.
func (e *acceptanceEnv) tinyRunArgs(arms string) []string {
	return []string{"run", "--task", "tiny", "--arms", arms,
		"--single-route", "pi | test-model | high",
		"--roster", "bench/roster.toml", "--docflow", e.docflow, "--timeout", "120"}
}

// runIDFromOutput reads the id from the run's final stdout line
// "<run-id>: N results, wrote bench/results/README.md".
func runIDFromOutput(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.Contains(line, " results, wrote ") {
			return line[:strings.Index(line, ":")]
		}
	}
	t.Fatalf("no run id line in %q", out)
	return ""
}

func (e *acceptanceEnv) rows() []benchResult {
	e.t.Helper()
	rows, err := readResults(filepath.Join(e.results, "runs.jsonl"))
	if err != nil {
		e.t.Fatal(err)
	}
	return rows
}

func (e *acceptanceEnv) lastCommitSubject() string {
	e.t.Helper()
	return strings.TrimSpace(gitBench(e.t, e.root, "log", "-1", "--pretty=%s"))
}

func (e *acceptanceEnv) lastCommitFiles() []string {
	e.t.Helper()
	out := strings.TrimSpace(gitBench(e.t, e.root, "show", "--pretty=format:", "--name-only", "HEAD"))
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func TestAcceptanceBenchmarkRun(t *testing.T) {
	e := newAcceptanceEnv(t)

	// Scenario 1: three arms end to end.
	out, errOut, code := e.run(e.tinyRunArgs("single,docflow,docflow-one")...)
	if code != 0 {
		t.Fatalf("run exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	runID := runIDFromOutput(t, out)
	rows := e.rows()
	if len(rows) != 3 {
		t.Fatalf("got %d result rows, want 3: %#v", len(rows), rows)
	}
	byArm := map[string]benchResult{}
	for _, row := range rows {
		if row.V != 1 || row.Task != "tiny" || row.TaskSHA == "" || row.RunID != runID {
			t.Fatalf("bad row: %#v", row)
		}
		byArm[row.Arm] = row
	}
	passed := oracleResult{Passed: 1, Failed: 0, Total: 1}

	single := byArm["single"]
	if single.Status != "done" || single.Oracle != passed {
		t.Fatalf("single arm: %#v", single)
	}
	if single.Route == nil || single.Route.Agent != "pi" || single.Route.Model != "test-model" || single.Route.Effort != "high" {
		t.Fatalf("single route: %#v", single.Route)
	}
	if single.Cost == nil || *single.Cost != 0.25 {
		t.Fatalf("single cost: %#v", single.Cost)
	}
	if single.Diff.Files != 1 || single.Diff.Insertions != 1 || single.Diff.Deletions != 1 {
		t.Fatalf("single diff = %+v, want one changed file with one insertion and one deletion", single.Diff)
	}
	if single.DocflowVersion == "" || single.DocflowCommit == "" || single.DocflowDirty {
		t.Fatalf("single environment fields: %#v", single)
	}

	docflowRow := byArm["docflow"]
	if docflowRow.Status != "done" || docflowRow.Oracle != passed {
		branch := filepath.Join(e.root, "work", "bench", runID, "tiny-docflow")
		log, _ := os.ReadFile(filepath.Join(branch, ".bench-docflow.log"))
		t.Fatalf("docflow arm: %#v\nlog:\n%s", docflowRow, log)
	}
	if docflowRow.Units.Total != 1 || docflowRow.Units.Done != 1 || docflowRow.Attempts < 1 {
		t.Fatalf("docflow units: %#v", docflowRow.Units)
	}
	if docflowRow.Cost == nil || *docflowRow.Cost <= 0 {
		t.Fatalf("docflow cost: %#v", docflowRow.Cost)
	}
	rosterSum := sha256.Sum256([]byte(acceptanceRoster))
	if docflowRow.RosterSHA != hex.EncodeToString(rosterSum[:]) || len(docflowRow.Chains) == 0 {
		t.Fatalf("docflow roster metadata: sha=%q chains=%v", docflowRow.RosterSHA, docflowRow.Chains)
	}

	one := byArm["docflow-one"]
	if one.Status != "done" || one.Oracle != passed || one.Units.Total != 1 || one.Units.Done != 1 {
		branch := filepath.Join(e.root, "work", "bench", runID, "tiny-docflow-one")
		log, _ := os.ReadFile(filepath.Join(branch, ".bench-docflow.log"))
		t.Fatalf("docflow-one arm: %#v\nlog:\n%s", one, log)
	}
	if one.Route == nil || one.Route.Model != "test-model" {
		t.Fatalf("docflow-one route: %#v", one.Route)
	}
	// The docflow-one rewrite keeps the route keys, so the plan survives lint.
	planText, err := os.ReadFile(filepath.Join(e.root, "work", "bench", runID, "tiny-docflow-one", "docs", "plans", "P001-tiny.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(planText), "executor: pi | test-model | high\n") {
		t.Fatalf("docflow-one plan lost its route keys:\n%s", planText)
	}

	summary, err := os.ReadFile(filepath.Join(e.results, runID, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, arm := range []string{"single", "docflow", "docflow-one"} {
		if !strings.Contains(string(summary), "| tiny | "+arm+" |") {
			t.Fatalf("summary lacks the %s arm row:\n%s", arm, summary)
		}
	}
	readme, err := os.ReadFile(filepath.Join(e.results, "README.md"))
	if err != nil || !strings.Contains(string(readme), "## tiny") {
		t.Fatalf("trend README: err=%v\n%s", err, readme)
	}
	if subject := e.lastCommitSubject(); subject != "bench: run "+runID {
		t.Fatalf("last commit = %q, want %q", subject, "bench: run "+runID)
	}
	for _, file := range e.lastCommitFiles() {
		if !strings.HasPrefix(filepath.ToSlash(file), "bench/results/") {
			t.Fatalf("result commit touched %q", file)
		}
	}

	// Scenario 2: a second run of the same task shows two rows and a delta.
	out, errOut, code = e.run(e.tinyRunArgs("single")...)
	if code != 0 {
		t.Fatalf("second run exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	runID2 := runIDFromOutput(t, out)
	if runID2 == runID {
		t.Fatal("second run reused the run id")
	}
	if rows = e.rows(); len(rows) != 4 {
		t.Fatalf("got %d result rows, want 4", len(rows))
	}
	readme, err = os.ReadFile(filepath.Join(e.results, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(readme), "| single |") != 2 || !strings.Contains(string(readme), "+0 pp") || !strings.Contains(string(readme), "+0.00") {
		t.Fatalf("trend lacks two single rows or zero deltas:\n%s", readme)
	}

	// Scenario 2b: a dirty results directory refuses the run with code 2.
	writeAcceptanceFile(t, e.results, "dirty.txt", "dirty\n", 0o644)
	before, err := os.ReadFile(filepath.Join(e.results, "runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	out, errOut, code = e.run(e.tinyRunArgs("single")...)
	if code != 2 || !strings.Contains(out+errOut, "uncommitted changes") {
		t.Fatalf("dirty-results run: exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	after, err := os.ReadFile(filepath.Join(e.results, "runs.jsonl"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("refused run changed results: err=%v", err)
	}
	if err := os.Remove(filepath.Join(e.results, "dirty.txt")); err != nil {
		t.Fatal(err)
	}
}

// TestAcceptanceBenchmarkInterrupt covers the Ctrl-C path: the interrupted
// arm is recorded with status interrupted and the partial run is committed.
func TestAcceptanceBenchmarkInterrupt(t *testing.T) {
	e := newAcceptanceEnv(t)
	writeAcceptanceFile(t, e.binDir, "pi", "#!/bin/sh\nsleep 60\n", 0o755)

	var out, errOut bytes.Buffer
	c := e.command(e.tinyRunArgs("single")...)
	c.Stdout, c.Stderr = &out, &errOut
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	var waitErr error

	// Wait until the arm's agent call has started, then interrupt.
	deadline := time.Now().Add(30 * time.Second)
	started := false
	for !started && time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("bench run exited before the arm started: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errOut.String())
		case <-time.After(100 * time.Millisecond):
		}
		matches, _ := filepath.Glob(filepath.Join(e.root, "work", "bench", "*", "tiny-single", ".bench-agent.log"))
		started = len(matches) > 0
	}
	if !started {
		_ = c.Process.Kill()
		t.Fatalf("arm did not start\nstdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
	if err := c.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		waitErr = err
	case <-time.After(60 * time.Second):
		_ = c.Process.Kill()
		t.Fatalf("bench run did not exit after SIGINT\nstdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
	code := 0
	if waitErr != nil {
		exit, ok := waitErr.(*exec.ExitError)
		if !ok {
			t.Fatal(waitErr)
		}
		code = exit.ExitCode()
	}
	if code != 130 {
		t.Fatalf("interrupted run exit %d, want 130\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	runID := runIDFromOutput(t, out.String())
	rows := e.rows()
	if len(rows) != 1 || rows[0].Arm != "single" || rows[0].Status != "interrupted" {
		t.Fatalf("interrupted results: %#v", rows)
	}
	if subject := e.lastCommitSubject(); subject != "bench: run "+runID {
		t.Fatalf("last commit = %q, want %q", subject, "bench: run "+runID)
	}
	if _, err := os.Stat(filepath.Join(e.results, runID, "summary.md")); err != nil {
		t.Fatalf("interrupted run summary: %v", err)
	}
}

// TestAcceptanceValidateCorpus runs validate over the real reference corpus.
func TestAcceptanceValidateCorpus(t *testing.T) {
	fixture, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	bin := nativeBin(t.TempDir(), "bench")
	build := exec.Command("go", "build", "-o", bin, "./tools/bench")
	build.Dir = fixture
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build bench: %v\n%s", err, output)
	}
	c := exec.Command(bin, "validate", "--task", "all")
	c.Dir = fixture
	c.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"XDG_CONFIG_HOME="+filepath.Join(t.TempDir(), "config"),
		"DOCFLOW_SKILLS_CHECK=0",
	)
	var out, errOut bytes.Buffer
	c.Stdout, c.Stderr = &out, &errOut
	err = c.Run()
	code := 0
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	if code != 0 {
		t.Fatalf("validate --task all exit %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("validate printed %d lines, want 3:\n%s", len(lines), out.String())
	}
	for _, name := range []string{"small", "medium", "large"} {
		found := false
		for _, line := range lines {
			if strings.HasPrefix(line, name+": base ") && strings.Contains(line, "solution ") && strings.HasSuffix(line, "lint ok") {
				found = true
			}
		}
		if !found {
			t.Fatalf("task %s not green:\n%s", name, out.String())
		}
	}
}

// TestAcceptanceOracleFairness closes the P051.2.6 handoff risk: an oracle
// must not punish a plan-conform implementation for details the plan leaves
// open. Each case copies the reference solution, changes only an unspecified
// detail, and requires the oracle to stay green. The flagged 4-column list
// layouts are closed from the other side: the medium and large plans now fix
// the exact list header, so an implementation without the column violates
// the plan, not a guess.
func TestAcceptanceOracleFairness(t *testing.T) {
	fixture, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(fixture, "bench", "base")
	cases := []struct {
		task      string
		mutations [][2]string // {anchor, replacement} pairs applied in order
	}{
		{
			// The plan fixes the field set, not the JSON key order:
			// marshal date last instead of first.
			task: "small",
			mutations: [][2]string{
				{"\t\t\tDate        string `json:\"date\"`\n\t\t\tAmount      int64  `json:\"amount\"`\n\t\t\tDescription string `json:\"description\"`\n",
					"\t\t\tDescription string `json:\"description\"`\n\t\t\tAmount      int64  `json:\"amount\"`\n\t\t\tDate        string `json:\"date\"`\n"},
				{"jsonEntry{entry.Date, entry.Amount, entry.Description}",
					"jsonEntry{Description: entry.Description, Amount: entry.Amount, Date: entry.Date}"},
			},
		},
		{
			// The plan fixes one row per category, not their order:
			// store budget rows reverse-sorted.
			task: "medium",
			mutations: [][2]string{
				{"sort.Strings(categories)", "sort.Sort(sort.Reverse(sort.StringSlice(categories)))"},
			},
		},
		{
			// The plan fixes compactness and the field set of export JSON,
			// not the key order: marshal description first.
			task: "large",
			mutations: [][2]string{
				{"row{e.Date, e.Amount, e.Currency, e.Description}",
					"row{Description: e.Description, Currency: e.Currency, Amount: e.Amount, Date: e.Date}"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.task, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "docflow-bench-fair-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			if err := materialize(base, dir); err != nil {
				t.Fatal(err)
			}
			if err := overlay(filepath.Join(fixture, "bench", "tasks", tc.task, "solution"), dir); err != nil {
				t.Fatal(err)
			}
			for _, mutation := range tc.mutations {
				path := filepath.Join(dir, "cmd", "ledger", "main.go")
				if tc.task == "large" {
					path = filepath.Join(dir, "internal", "journal", "store.go")
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if count := strings.Count(string(data), mutation[0]); count != 1 {
					t.Fatalf("mutation anchor appears %d times in %s", count, path)
				}
				if err := os.WriteFile(path, []byte(strings.Replace(string(data), mutation[0], mutation[1], 1)), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := overlay(filepath.Join(fixture, "bench", "tasks", tc.task, "oracle"), dir); err != nil {
				t.Fatal(err)
			}
			counts, _ := runOracleDetailed(dir)
			if counts.total == 0 || counts.passed != counts.total {
				t.Fatalf("oracle punishes a plan-conform %s implementation: %d/%d passed", tc.task, counts.passed, counts.total)
			}
		})
	}
}

// TestAcceptanceRouteRewrite pins the docflow-one header rewrite: the route
// keys must survive, a bare route value fails lint inside docflow run.
func TestAcceptanceRouteRewrite(t *testing.T) {
	plan := "executor: codex | gpt-6-luna | high\nfallback: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high\nreview: pi | zai/glm-5.3 | max\n\n## Goal\n\nText.\n"
	got := rewritePlanRoutes(plan, agents.Route{Agent: "pi", Model: "test-model", Effort: "high"})
	want := "executor: pi | test-model | high\nreview: pi | test-model | high\n\n## Goal\n\nText.\n"
	if got != want {
		t.Fatalf("rewrite = %q, want %q", got, want)
	}
}

// TestAcceptanceShortStat covers the singular and partial forms of
// `git diff --shortstat` output that a single-file diff produces.
func TestAcceptanceShortStat(t *testing.T) {
	cases := []struct {
		text string
		want diffResult
	}{
		{"1 file changed, 1 insertion(+), 1 deletion(-)", diffResult{Files: 1, Insertions: 1, Deletions: 1}},
		{"3 files changed, 12 insertions(+), 4 deletions(-)", diffResult{Files: 3, Insertions: 12, Deletions: 4}},
		{"2 files changed, 3 insertions(+)", diffResult{Files: 2, Insertions: 3}},
		{"1 file changed, 2 deletions(-)", diffResult{Files: 1, Deletions: 2}},
		{"", diffResult{}},
	}
	for _, c := range cases {
		if got := parseShortStat(c.text); got != c.want {
			t.Errorf("parseShortStat(%q) = %+v, want %+v", c.text, got, c.want)
		}
	}
}

// A docflow timeout must reap the nested agent and its children before
// scoring and committing the partial result.
func TestAcceptanceDocflowTimeout(t *testing.T) {
	e := newAcceptanceEnv(t)
	writeAcceptanceFile(t, e.binDir, "pi", `#!/bin/sh
case "$*" in *'auth print-api-key'*) exit 1 ;; esac
cat >/dev/null
sleep 60 &
echo $! > child.pid
wait
`, 0o755)
	args := append(e.tinyRunArgs("docflow"), "--timeout", "2")
	started := time.Now()
	out, errOut, code := e.run(args...)
	if code != 0 || time.Since(started) > 25*time.Second {
		t.Fatalf("timeout exit %d, elapsed %s\n%s\n%s", code, time.Since(started), out, errOut)
	}
	rows := e.rows()
	if len(rows) != 1 || rows[0].Status != "timeout" {
		t.Fatalf("timeout rows = %+v", rows)
	}
	runID := runIDFromOutput(t, out)
	pidPath := filepath.Join(e.root, "work", "bench", runID, "tiny-docflow", "child.pid")
	pid, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("sh", "-c", `kill -0 "$1" 2>/dev/null`, "sh", strings.TrimSpace(string(pid))).Run(); err == nil {
		t.Fatalf("agent child %s survived timeout", pid)
	}
	if got := e.lastCommitSubject(); got != "bench: run "+runID {
		t.Fatalf("partial results not committed: %s", got)
	}
}

func TestAcceptanceCustomResults(t *testing.T) {
	e := newAcceptanceEnv(t)
	args := append(e.tinyRunArgs("single"), "--results", "bench/custom-results")
	out, errOut, code := e.run(args...)
	if code != 0 {
		t.Fatalf("custom results exit %d\n%s\n%s", code, out, errOut)
	}
	rows, err := readResults(filepath.Join(e.root, "bench", "custom-results", "runs.jsonl"))
	if err != nil || len(rows) != 1 || rows[0].Status != "done" {
		t.Fatalf("custom rows = %+v, err=%v", rows, err)
	}
	for _, file := range e.lastCommitFiles() {
		if !strings.HasPrefix(filepath.ToSlash(file), "bench/custom-results/") {
			t.Fatalf("unexpected commit path %s", file)
		}
	}
}
