package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/roster"
)

const defaultSingleRoute = "codex | gpt-6.1-sol | high"

type runOptions struct {
	task, arms, singleRoute, rosterPath, docflowPath, resultsPath string
	timeout                                                       time.Duration
	dryRun                                                        bool
}

func runCommand(args []string, out io.Writer) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	return runCommandAt(root, args, out)
}

func runCommandAt(root string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	opt := runOptions{}
	flags.StringVar(&opt.task, "task", "", "task name or all")
	flags.StringVar(&opt.resultsPath, "results", "bench/results", "results directory inside the checkout")
	flags.StringVar(&opt.arms, "arms", "single,docflow", "comma-separated benchmark arms")
	flags.StringVar(&opt.singleRoute, "single-route", defaultSingleRoute, "single route: agent | model | effort")
	flags.StringVar(&opt.rosterPath, "roster", "bench/roster.toml", "benchmark roster")
	flags.StringVar(&opt.docflowPath, "docflow", "", "docflow executable")
	seconds := flags.Int("timeout", 21600, "seconds per arm")
	flags.BoolVar(&opt.dryRun, "dry-run", false, "show configuration without running agents")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || opt.task == "" || *seconds < 1 {
		return &exitError{code: 2, message: "usage: bench run --task NAME|all [--arms single,docflow] [--single-route \"agent | model | effort\"] [--roster bench/roster.toml] [--docflow PATH] [--timeout SECONDS] [--dry-run]"}
	}
	opt.timeout = time.Duration(*seconds) * time.Second
	requestedArms, err := parseArms(opt.arms)
	if err != nil {
		return err
	}
	route, err := parseRoute(opt.singleRoute)
	if err != nil {
		return err
	}
	tasksDir := filepath.Join(root, "bench", "tasks")
	baseDir := filepath.Join(root, "bench", "base")
	names, err := taskNames(tasksDir, opt.task)
	if err != nil {
		return err
	}
	resultsRel, err := filepath.Rel(root, resolve(root, opt.resultsPath))
	if err != nil || resultsRel == "." || resultsRel == ".." || strings.HasPrefix(resultsRel, ".."+string(filepath.Separator)) {
		return &exitError{code: 2, message: "results must name a directory inside the checkout"}
	}
	rm, err := rosterMetadata(root, resolve(root, opt.rosterPath))
	if err != nil {
		return err
	}
	if opt.dryRun {
		fmt.Fprintf(out, "tasks: %s\narms: %s\nsingle: %s\n", strings.Join(names, ", "), strings.Join(requestedArms, ", "), routeText(route))
		fmt.Fprintln(out, "roster:")
		fmt.Fprint(out, rm.value.Show())
		fmt.Fprintf(out, "base: %s\nwork: work/bench/<run-id>\nresults: %s\n", filepath.ToSlash(filepath.Join("bench", "base")), filepath.ToSlash(resultsRel))
		for _, name := range names {
			for _, arm := range requestedArms {
				fmt.Fprintf(out, "path: work/bench/<run-id>/%s-%s\n", name, arm)
			}
		}
		return nil
	}
	if err := ensureResultsClean(root, resultsRel); err != nil {
		return err
	}
	resultsDir := filepath.Join(root, resultsRel)
	start := time.Now()
	runID, err := resultRunID(start, resultsDir)
	if err != nil {
		return err
	}
	workDir := filepath.Join(root, "work", "bench", runID)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	docflowBin, docflowVersion, docflowCommit, docflowDirty, err := benchmarkDocflow(root, workDir, opt.docflowPath)
	if err != nil {
		return err
	}
	taskRecords := make([]taskInput, 0, len(names))
	for _, name := range names {
		input, err := loadTask(root, baseDir, tasksDir, name)
		if err != nil {
			return err
		}
		taskRecords = append(taskRecords, input)
	}
	var rows []benchResult
	oracleTails := map[string][]string{}
	interrupted := false
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for _, input := range taskRecords {
		for _, arm := range requestedArms {
			if ctx.Err() != nil {
				interrupted = true
				break
			}
			row, tail := executeArm(ctx, root, workDir, docflowBin, docflowVersion, docflowCommit, docflowDirty, rm.value, route, input, arm, opt.timeout)
			row.V, row.RunID, row.TS, row.Task, row.TaskSHA, row.Arm = 1, runID, float64(time.Now().Unix()), input.name, input.digest, arm
			if arm == "docflow" {
				row.RosterSHA, row.Chains = rm.sha, rm.chains
			}
			if arm == "single" || arm == "docflow-one" {
				row.Route = &benchRoute{Agent: route.Agent, Model: route.Model, Effort: route.Effort}
			}
			row.DocflowVersion, row.DocflowCommit, row.DocflowDirty = docflowVersion, docflowCommit, docflowDirty
			rows = append(rows, row)
			if len(tail) > 0 {
				oracleTails[input.name+"/"+arm] = tail
			}
			if err := appendResult(resultsDir, row); err != nil {
				return err
			}
			if row.Status == "interrupted" {
				interrupted = true
				break
			}
		}
		if interrupted {
			break
		}
	}
	if err := os.MkdirAll(filepath.Join(resultsDir, runID), 0o755); err != nil {
		return err
	}
	if err := writeRunSummary(filepath.Join(resultsDir, runID), rows, oracleTails); err != nil {
		return err
	}
	if err := writeReportAt(resultsDir); err != nil {
		return err
	}
	if err := commitResults(root, resultsRel, runID); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %d results, wrote %s\n", runID, len(rows), filepath.ToSlash(filepath.Join(resultsRel, "README.md")))
	if interrupted {
		return &exitError{code: 130, message: "benchmark interrupted; partial results recorded"}
	}
	return nil
}

func writeReportAt(dir string) error {
	rows, err := readResults(filepath.Join(dir, "runs.jsonl"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "README.md"), []byte(renderTrend(rows)), 0o644)
}

type loadedRoster struct {
	sha    string
	chains []string
	value  *roster.Roster
}

func parseArms(value string) ([]string, error) {
	valid := map[string]bool{"single": true, "docflow": true, "docflow-one": true}
	var arms []string
	seen := map[string]bool{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if !valid[item] || seen[item] {
			return nil, fmt.Errorf("invalid or duplicate arm %q", item)
		}
		seen[item] = true
		arms = append(arms, item)
	}
	if len(arms) == 0 {
		return nil, fmt.Errorf("at least one arm is required")
	}
	return arms, nil
}

func parseRoute(value string) (agents.Route, error) {
	parts := strings.Split(value, "|")
	if len(parts) != 3 {
		return agents.Route{}, fmt.Errorf("route must be \"agent | model | effort\"")
	}
	r := agents.Route{Agent: strings.TrimSpace(parts[0]), Model: strings.TrimSpace(parts[1]), Effort: strings.TrimSpace(parts[2])}
	if r.Agent == "" || r.Model == "" || r.Effort == "" {
		return agents.Route{}, fmt.Errorf("route must include agent, model, and effort")
	}
	return r, nil
}

func routeText(route agents.Route) string {
	return route.Agent + " | " + route.Model + " | " + route.Effort
}

type taskInput struct {
	name, dir, plan, digest string
	base                    string
}

func loadTask(root, baseDir, tasksDir, name string) (taskInput, error) {
	taskDir := filepath.Join(tasksDir, name)
	planPath := filepath.Join(taskDir, "plan.md")
	plan, err := os.ReadFile(planPath)
	if err != nil {
		return taskInput{}, fmt.Errorf("%s: read plan: %w", name, err)
	}
	digest, err := taskDigest(planPath, filepath.Join(taskDir, "oracle"), filepath.Join(taskDir, "solution"), baseDir)
	if err != nil {
		return taskInput{}, err
	}
	return taskInput{name: name, dir: taskDir, plan: string(plan), digest: digest, base: baseDir}, nil
}

func benchmarkDocflow(root, work string, supplied string) (string, string, string, bool, error) {
	gitHead, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		return "", "", "", false, err
	}
	dirty, err := gitOutput(root, "status", "--porcelain")
	if err != nil {
		return "", "", "", false, err
	}
	bin := supplied
	if bin == "" {
		bin = filepath.Join(work, "bin", "docflow")
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			return "", "", "", false, err
		}
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/docflow")
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", "", "", false, fmt.Errorf("build docflow: %s: %w", strings.TrimSpace(string(output)), err)
		}
	} else if !filepath.IsAbs(bin) {
		bin = resolve(root, bin)
	}
	version, err := commandOutput(root, bin, "--version")
	if err != nil {
		return "", "", "", false, fmt.Errorf("docflow --version: %w", err)
	}
	return bin, strings.TrimSpace(version), strings.TrimSpace(gitHead), strings.TrimSpace(dirty) != "", nil
}

func executeArm(ctx context.Context, root, work, docflowBin, version, commit string, dirty bool, rosterValue *roster.Roster, route agents.Route, task taskInput, arm string, timeout time.Duration) (benchResult, []string) {
	started := time.Now()
	branch := filepath.Join(work, task.name+"-"+arm)
	result := benchResult{Status: "error", Cost: nil, Unverified: []string{}}
	_ = os.RemoveAll(branch)
	if err := materialize(task.base, branch); err != nil {
		result.Status = "error"
		return finalizeArm(branch, task, arm, result, started, "")
	}
	// The materialized base commit has no ref; diff by its hash.
	baseCommit, err := gitOutput(branch, "rev-parse", "HEAD")
	if err != nil {
		result.Status = "error"
		return finalizeArm(branch, task, arm, result, started, "")
	}
	if arm == "single" {
		runSingle(ctx, root, branch, task.plan, route, timeout, &result)
	} else {
		runDocflow(ctx, branch, docflowBin, rosterValue, route, task, arm == "docflow-one", timeout, &result)
	}
	return finalizeArm(branch, task, arm, result, started, strings.TrimSpace(baseCommit))
}

func finalizeArm(branch string, task taskInput, arm string, result benchResult, started time.Time, baseCommit string) (benchResult, []string) {
	result.Seconds = time.Since(started).Seconds()
	result.Diff = diffFromBase(branch, baseCommit)
	if _, err := os.Stat(filepath.Join(task.dir, "oracle")); err != nil {
		result.Status = "error"
		return result, nil
	}
	if err := overlay(filepath.Join(task.dir, "oracle"), branch); err != nil {
		result.Status = "error"
		return result, nil
	}
	counts, tail := runOracleDetailed(branch)
	result.Oracle = oracleResult{Passed: counts.passed, Total: counts.total, Failed: counts.total - counts.passed}
	return result, tail
}

func runSingle(parent context.Context, root, branch, plan string, route agents.Route, timeout time.Duration, result *benchResult) {
	prompt := "Implement the entire task described below in this session. Do not create a docflow plan. Run `go test ./...` before finishing.\n\n" + stripPlanForSingle(plan)
	config, err := agents.LoadAgents("", false)
	if err != nil {
		result.Status = "error"
		return
	}
	profiles, _ := config["agents"].(map[string]any)
	profile, _ := profiles[route.Agent].(map[string]any)
	if profile == nil {
		result.Status = "error"
		return
	}
	argv, stdin, err := agents.BuildCommand(profile, prompt, route.Model, "", "", route.Effort, "auto")
	if err != nil {
		result.Status = "error"
		return
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	run, err := agentio.CallAgent(branch, argv, stdin, filepath.Join(branch, ".bench-agent.log"), agentio.CallOptions{Timeout: timeout, Context: ctx})
	result.Cost = agentio.RunCost(profile, run)
	if tokens := agentio.RunTokens(profile, run); tokens != nil {
		result.Tokens = *tokens
	}
	if parent.Err() != nil {
		result.Status = "interrupted"
		return
	}
	if run.TimedOut || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		result.Status = "timeout"
		return
	}
	if err != nil || run.Code != 0 {
		result.Status = "error"
		return
	}
	result.Status = "done"
}

var unitStatusRE = regexp.MustCompile(`^### \[([ x!~])\] `)

func stripPlanForSingle(plan string) string {
	lines := strings.Split(plan, "\n")
	var out []string
	inCycle := false
	for _, line := range lines {
		if strings.HasPrefix(line, "status:") {
			continue
		}
		if strings.HasPrefix(line, "## Cycle") {
			inCycle = true
			continue
		}
		if inCycle {
			if strings.HasPrefix(line, "## ") {
				inCycle = false
			} else {
				continue
			}
		}
		if unitStatusRE.MatchString(line) {
			line = unitStatusRE.ReplaceAllString(line, "### ")
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func runDocflow(parent context.Context, branch, binary string, rosterValue *roster.Roster, route agents.Route, task taskInput, one bool, timeout time.Duration, result *benchResult) {
	if _, err := commandOutput(branch, binary, "init", "--agent", "codex"); err != nil {
		result.Status = "error"
		return
	}
	docflowPath := filepath.Join(branch, "docs", "docflow.md")
	text, err := os.ReadFile(docflowPath)
	if err != nil {
		result.Status = "error"
		return
	}
	text = []byte(strings.Replace(string(text), "## Queue\n", "## Queue\n- P001\n", 1))
	if err := os.WriteFile(docflowPath, text, 0o644); err != nil {
		result.Status = "error"
		return
	}
	planPath := filepath.Join(branch, "docs", "plans", "P001-"+task.name+".md")
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		result.Status = "error"
		return
	}
	if err := os.WriteFile(planPath, []byte(task.plan), 0o644); err != nil {
		result.Status = "error"
		return
	}
	if err := gitAddCommit(branch, "benchmark plan"); err != nil {
		result.Status = "error"
		return
	}
	if output, err := commandOutput(branch, binary, "roster", "apply", "P001", "--roster", rosterValue.Path); err != nil {
		_ = os.WriteFile(filepath.Join(branch, ".bench-docflow.log"), []byte(redactAbsolutePaths(output)), 0o644)
		result.Status = "error"
		return
	}
	if one {
		planText, err := os.ReadFile(planPath)
		if err != nil {
			result.Status = "error"
			return
		}
		if err := os.WriteFile(planPath, []byte(rewritePlanRoutes(string(planText), route)), 0o644); err != nil {
			result.Status = "error"
			return
		}
	}
	if err := gitAddCommit(branch, "apply benchmark roster"); err != nil {
		result.Status = "error"
		return
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	run, err := agentio.CallAgent(branch, []string{binary, "run", "--yes"}, nil, "", agentio.CallOptions{Context: ctx, Timeout: timeout})
	out := []byte(run.Stdout + "\n" + run.Stderr)
	_ = os.WriteFile(filepath.Join(branch, ".bench-docflow.log"), out, 0o644)

	if parent.Err() != nil {
		result.Status = "interrupted"
	} else if ctx.Err() == context.DeadlineExceeded {
		result.Status = "timeout"
	} else if run.TimedOut {
		result.Status = "timeout"
	} else if err != nil {
		result.Status = "error"
	} else if !docflowExitCompleted(run.Code, out) {
		result.Status = "failed"
	} else {
		result.Status = "done"
	}
	collectRunMetrics(branch, result)
}

func docflowExitCompleted(code int, output []byte) bool {
	if code == 0 {
		return true
	}
	if code != 1 {
		return false
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(line) == "nothing to do" {
			return true
		}
	}
	return false
}

var routeHeader = regexp.MustCompile(`(?m)^(executor|cheap|standard|strong|heavy|review|acceptance|replan):.*`)
var fallbackHeader = regexp.MustCompile(`(?m)^fallback:.*\n?`)

// rewritePlanRoutes points every route key of a plan header at one route and
// drops the fallback chain, keeping the key names: a bare route value would
// break the frontmatter and fail lint in the docflow run.
func rewritePlanRoutes(plan string, route agents.Route) string {
	text := routeHeader.ReplaceAllString(plan, "${1}: "+routeText(route))
	return fallbackHeader.ReplaceAllString(text, "")
}

func collectRunMetrics(branch string, result *benchResult) {
	gitDir, err := gitOutput(branch, "rev-parse", "--git-dir")
	if err != nil {
		return
	}
	eventsDir := filepath.Join(branch, strings.TrimSpace(gitDir), "docflow", "run")
	config, _ := agents.LoadAgents("", false)
	profiles, _ := config["agents"].(map[string]any)
	var callCost, unitCost *float64
	entries, _ := os.ReadDir(eventsDir)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".events.jsonl") {
			continue
		}
		file, err := os.Open(filepath.Join(eventsDir, entry.Name()))
		if err != nil {
			continue
		}
		s := bufio.NewScanner(file)
		for s.Scan() {
			var event map[string]any
			if json.Unmarshal(s.Bytes(), &event) != nil {
				continue
			}
			kind := stringField(event, "event", "type", "kind")
			switch kind {
			case "run_start":
				result.Units.Total = intField(event, "units")
			case "unit_end":
				if event["status"] == "done" {
					result.Units.Done++
				}
				if event["status"] == "blocked" {
					result.Units.Blocked++
				}
			case "attempt":
				result.Attempts++
				route, _ := event["route"].(map[string]any)
				profile, _ := profiles[stringField(route, "agent")].(map[string]any)
				log, err := os.ReadFile(filepath.Join(branch, filepath.FromSlash(stringField(event, "log"))))
				if err == nil && profile != nil {
					stdout := string(log)
					if _, after, ok := strings.Cut(stdout, "--- stdout\n"); ok {
						stdout = after
					}
					stdout, _, _ = strings.Cut(stdout, "\n--- stderr\n")
					call := agentio.AgentRun{Stdout: stdout}
					callCost = agentio.Add(callCost, agentio.RunCost(profile, call))
					if tokens := agentio.RunTokens(profile, call); tokens != nil {
						result.Tokens += *tokens
					}
				}
			case "replan":
				result.Replans++
				result.CheckChanges += sliceLen(event["check_changes"])
			case "commit":
				result.TestChanges += sliceLen(event["test_changes"])
			}
			if kind == "unit_end" {
				if value, ok := event["cost"].(float64); ok {
					unitCost = agentio.Add(unitCost, &value)
				}
			}
		}
		file.Close()
	}
	result.Cost = callCost
	if result.Cost == nil {
		result.Cost = unitCost
	}
	roots, _ := gitOutput(branch, "rev-list", "--max-parents=0", "HEAD")
	base := strings.TrimSpace(roots)
	commits, _ := gitOutput(branch, "log", "--format=%B", "--extended-regexp", "--grep=^P[0-9]+\\.[0-9]+\\.[0-9]+:", base+"..HEAD")
	for _, line := range strings.Split(commits, "\n") {
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if strings.HasPrefix(trimmed, "Unverified:") {
			result.Unverified = append(result.Unverified, trimmed)
		}
	}
}

func stringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok {
			return s
		}
	}
	return ""
}
func intField(m map[string]any, keys ...string) int {
	for _, k := range keys {
		switch v := m[k].(type) {
		case float64:
			return int(v)
		case string:
			n, _ := strconv.Atoi(v)
			return n
		}
	}
	return 0
}

func sliceLen(value any) int {
	if items, ok := value.([]any); ok {
		return len(items)
	}
	return 0
}

func runOracleDetailed(root string) (testCounts, []string) {
	argv := cycle.PlanShell("go test -json ./benchoracle/...")
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = root
	output, _ := cmd.CombinedOutput()
	counts := parseGoTestJSON(output)
	if counts.total == 0 {
		counts.total = oracleTestCount(filepath.Join(root, "benchoracle"))
	}
	type oracleEvent struct{ Action, Package, Test, Output string }
	var events []oracleEvent
	failed := map[string]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		var event oracleEvent
		if json.Unmarshal([]byte(line), &event) == nil {
			events = append(events, event)
			if event.Action == "fail" {
				failed[event.Package+"/"+event.Test] = true
			}
		} else if strings.TrimSpace(line) != "" {
			events = append(events, oracleEvent{Output: line + "\n"})
		}
	}
	var tail []string
	if len(failed) > 0 || counts.passed != counts.total {
		for _, event := range events {
			if event.Output != "" && (event.Test == "" || failed[event.Package+"/"+event.Test]) {
				tail = append(tail, strings.Split(strings.TrimRight(redactAbsolutePaths(event.Output), "\n"), "\n")...)
			}
		}
	}
	return counts, tail
}

var (
	filesChangedRE = regexp.MustCompile(`(\d+) files? changed`)
	insertionsRE   = regexp.MustCompile(`(\d+) insertions?\(\+\)`)
	deletionsRE    = regexp.MustCompile(`(\d+) deletions?\(-\)`)
)

// parseShortStat reads a `git diff --shortstat` line, tolerating the
// singular forms and missing sections ("1 file changed, 1 insertion(+)").
func parseShortStat(text string) diffResult {
	var d diffResult
	if m := filesChangedRE.FindStringSubmatch(text); m != nil {
		d.Files, _ = strconv.Atoi(m[1])
	}
	if m := insertionsRE.FindStringSubmatch(text); m != nil {
		d.Insertions, _ = strconv.Atoi(m[1])
	}
	if m := deletionsRE.FindStringSubmatch(text); m != nil {
		d.Deletions, _ = strconv.Atoi(m[1])
	}
	return d
}

func diffFromBase(branch, baseCommit string) diffResult {
	if baseCommit == "" {
		return diffResult{}
	}
	paths := []string{".", ":(exclude)benchoracle/**", ":(exclude).bench-*"}
	if _, err := gitOutput(branch, append([]string{"add", "-A", "--"}, paths...)...); err != nil {
		return diffResult{}
	}
	out, err := gitOutput(branch, append([]string{"diff", "--cached", "--shortstat", baseCommit, "--"}, paths...)...)
	if err != nil {
		return diffResult{}
	}
	return parseShortStat(strings.TrimSpace(out))
}

func gitOutput(dir string, args ...string) (string, error) { return commandOutput(dir, "git", args...) }

func commandOutput(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %s: %w", name, strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}

func gitAddCommit(dir, message string) error {
	if _, err := gitOutput(dir, "add", "-A"); err != nil {
		return err
	}
	_, err := gitOutput(dir, "commit", "-m", message)
	return err
}

func rosterMetadata(root, path string) (loadedRoster, error) {
	r, err := roster.Load(path, "")
	if err != nil {
		return loadedRoster{}, err
	}
	data, err := os.ReadFile(r.Path)
	if err != nil {
		return loadedRoster{}, err
	}
	sum := sha256.Sum256(data)
	var chains []string
	for _, line := range strings.Split(strings.TrimSpace(r.Show()), "\n") {
		if strings.Contains(line, ": ") && !strings.HasPrefix(line, "spend:") {
			chains = append(chains, line)
		}
	}
	return loadedRoster{sha: hex.EncodeToString(sum[:]), chains: chains, value: r}, nil
}
