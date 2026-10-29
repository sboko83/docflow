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
	task, arms, singleRoute, rosterPath, docflowPath string
	timeout                                          time.Duration
	dryRun                                           bool
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
	rm, err := rosterMetadata(root, resolve(root, opt.rosterPath))
	if err != nil {
		return err
	}
	if opt.dryRun {
		fmt.Fprintf(out, "tasks: %s\narms: %s\nsingle: %s\n", strings.Join(names, ", "), strings.Join(requestedArms, ", "), routeText(route))
		fmt.Fprintln(out, "roster:")
		fmt.Fprint(out, rm.value.Show())
		fmt.Fprintf(out, "base: %s\nwork: work/bench/<run-id>\nresults: %s\n", filepath.ToSlash(filepath.Join("bench", "base")), filepath.ToSlash(filepath.Join("bench", "results")))
		for _, name := range names {
			for _, arm := range requestedArms {
				fmt.Fprintf(out, "path: work/bench/<run-id>/%s-%s\n", name, arm)
			}
		}
		return nil
	}
	resultsRel := "bench/results"
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
				row := benchResult{Status: "interrupted", Cost: nil, Unverified: []string{}}
				row.V, row.RunID, row.TS, row.Task, row.TaskSHA, row.Arm = 1, runID, float64(time.Now().Unix()), input.name, input.digest, arm
				row.RosterSHA, row.Chains = rm.sha, rm.chains
				row.DocflowVersion, row.DocflowCommit, row.DocflowDirty = docflowVersion, docflowCommit, docflowDirty
				rows = append(rows, row)
				if err := appendResult(resultsDir, row); err != nil {
					return err
				}
				interrupted = true
				break
			}
			row, tail := executeArm(ctx, root, workDir, docflowBin, docflowVersion, docflowCommit, docflowDirty, rm.value, route, input, arm, opt.timeout)
			row.V, row.RunID, row.TS, row.Task, row.TaskSHA, row.Arm = 1, runID, float64(time.Now().Unix()), input.name, input.digest, arm
			row.RosterSHA = rm.sha
			row.Chains = rm.chains
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
		return finalizeArm(root, branch, task, arm, result, started)
	}
	if arm == "single" {
		runSingle(ctx, root, branch, task.plan, route, timeout, &result)
	} else {
		runDocflow(ctx, branch, docflowBin, rosterValue, route, task, arm == "docflow-one", timeout, &result)
	}
	return finalizeArm(root, branch, task, arm, result, started)
}

func finalizeArm(root, branch string, task taskInput, arm string, result benchResult, started time.Time) (benchResult, []string) {
	result.Seconds = time.Since(started).Seconds()
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
	result.Diff = diffFromBase(branch)
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
		text := routeHeader.ReplaceAllString(string(planText), routeText(route)+"\n")
		text = fallbackHeader.ReplaceAllString(text, "")
		if err := os.WriteFile(planPath, []byte(text), 0o644); err != nil {
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
	cmd := exec.CommandContext(ctx, binary, "run", "--yes")
	cmd.Dir = branch
	out, err := cmd.CombinedOutput()
	_ = os.WriteFile(filepath.Join(branch, ".bench-docflow.log"), []byte(redactAbsolutePaths(string(out))), 0o644)
	if parent.Err() != nil {
		result.Status = "interrupted"
	} else if ctx.Err() == context.DeadlineExceeded {
		result.Status = "timeout"
	} else if !docflowRunCompleted(err, out) {
		result.Status = "error"
	} else {
		result.Status = "done"
	}
	collectRunMetrics(branch, result)
}

func docflowRunCompleted(err error, output []byte) bool {
	if err == nil {
		return true
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		return false
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(line) == "nothing to do" {
			return true
		}
	}
	return false
}

var routeHeader = regexp.MustCompile(`(?m)^(?:executor|cheap|standard|strong|heavy|review|acceptance|replan):.*\n`)
var fallbackHeader = regexp.MustCompile(`(?m)^fallback:.*\n`)

func collectRunMetrics(branch string, result *benchResult) {
	gitDir, err := gitOutput(branch, "rev-parse", "--git-dir")
	if err != nil {
		return
	}
	eventsDir := filepath.Join(branch, strings.TrimSpace(gitDir), "docflow", "run")
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
			case "unit_end":
				result.Units.Total++
				if event["status"] == "done" {
					result.Units.Done++
				}
				if event["status"] == "blocked" {
					result.Units.Blocked++
				}
			case "attempt":
				result.Attempts++
			case "replan":
				result.Replans++
				result.CheckChanges += sliceLen(event["check_changes"])
			case "commit":
				result.TestChanges += sliceLen(event["test_changes"])
			}
			if value, ok := event["cost"].(float64); ok {
				if result.Cost == nil {
					result.Cost = new(float64)
				}
				*result.Cost += value
			}
			result.Tokens += intField(event, "tokens", "total_tokens")
		}
		file.Close()
	}
	commits, _ := gitOutput(branch, "log", "--format=%B", "base..HEAD")
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
	var tail []string
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, `"Action":"fail"`) || strings.Contains(line, `"Output"`) {
			tail = append(tail, redactAbsolutePaths(line))
		}
	}
	return counts, tail
}

func diffFromBase(branch string) diffResult {
	out, err := gitOutput(branch, "diff", "--shortstat", "base", "--", ".", ":(exclude)benchoracle/**")
	if err != nil {
		return diffResult{}
	}
	var d diffResult
	fmt.Sscanf(strings.TrimSpace(out), "%d files changed, %d insertions(+), %d deletions(-)", &d.Files, &d.Insertions, &d.Deletions)
	return d
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
